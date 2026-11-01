// Package qdrant implements search.Index on top of a Qdrant collection with a named dense
// vector and a named IDF-weighted sparse vector, fused with reciprocal rank fusion.
package qdrant

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/qdrant/go-client/qdrant"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/search"
)

const (
	denseVector  = "dense"
	sparseVector = "lexical"
	// idField keeps the caller's ID in the payload, since Qdrant point IDs must be UUIDs or integers.
	idField = "_id"
	// prefetchFactor widens each branch before fusion so RRF has enough candidates.
	prefetchFactor = 4
)

type Config struct {
	Host   string
	Port   int
	APIKey string
	UseTLS bool
	// Collection is created on first use if it does not exist.
	Collection string
	// KeywordFields get a keyword payload index for filtering.
	KeywordFields []string
}

type Index struct {
	client     *qdrant.Client
	collection string
}

func New(cfg Config) (*Index, error) {
	client, err := qdrant.NewClient(&qdrant.Config{
		Host:                   cfg.Host,
		Port:                   cfg.Port,
		APIKey:                 cfg.APIKey,
		UseTLS:                 cfg.UseTLS,
		SkipCompatibilityCheck: true,
		GrpcOptions:            []grpc.DialOption{grpc.WithStatsHandler(otelgrpc.NewClientHandler())},
	})
	if err != nil {
		return nil, fmt.Errorf("connect qdrant: %w", err)
	}
	return &Index{client: client, collection: cfg.Collection}, nil
}

func (i *Index) Close() error {
	return i.client.Close()
}

func (i *Index) Ping(ctx context.Context) error {
	_, err := i.client.HealthCheck(ctx)
	return err
}

// ErrDimensionMismatch is returned by EnsureCollection when the existing collection was built
// with a different embedding size; the collection must be recreated (reindexed).
var ErrDimensionMismatch = errors.New("qdrant collection vector size does not match embedding dimension")

// EnsureCollection creates the collection and payload indexes if missing and verifies the dense size.
func (i *Index) EnsureCollection(ctx context.Context, dim int, keywordFields []string) error {
	exists, err := i.client.CollectionExists(ctx, i.collection)
	if err != nil {
		return fmt.Errorf("check collection: %w", err)
	}

	if exists {
		info, err := i.client.GetCollectionInfo(ctx, i.collection)
		if err != nil {
			return fmt.Errorf("get collection info: %w", err)
		}
		params := info.GetConfig().GetParams().GetVectorsConfig().GetParamsMap().GetMap()[denseVector]
		if params == nil || params.GetSize() != uint64(dim) {
			return fmt.Errorf("%w: collection %q has %d, embedder has %d", ErrDimensionMismatch, i.collection, params.GetSize(), dim)
		}
	} else {
		err = i.client.CreateCollection(ctx, &qdrant.CreateCollection{
			CollectionName: i.collection,
			VectorsConfig: qdrant.NewVectorsConfigMap(map[string]*qdrant.VectorParams{
				denseVector: {Size: uint64(dim), Distance: qdrant.Distance_Cosine},
			}),
			SparseVectorsConfig: qdrant.NewSparseVectorsConfig(map[string]*qdrant.SparseVectorParams{
				sparseVector: {Modifier: qdrant.Modifier_Idf.Enum()},
			}),
		})
		if err != nil {
			return fmt.Errorf("create collection: %w", err)
		}
	}

	for _, field := range append([]string{idField}, keywordFields...) {
		_, err := i.client.CreateFieldIndex(ctx, &qdrant.CreateFieldIndexCollection{
			CollectionName: i.collection,
			FieldName:      field,
			FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
			Wait:           qdrant.PtrOf(true),
		})
		if err != nil {
			return fmt.Errorf("create payload index %q: %w", field, err)
		}
	}
	return nil
}

// pointID maps an arbitrary string ID to a stable UUID (Qdrant requires UUID or integer IDs).
func pointID(id string) *qdrant.PointId {
	if u, err := uuid.Parse(id); err == nil {
		return qdrant.NewIDUUID(u.String())
	}
	return qdrant.NewIDUUID(uuid.NewSHA1(uuid.NameSpaceOID, []byte(id)).String())
}

func (i *Index) Upsert(ctx context.Context, points []search.Point) error {
	if len(points) == 0 {
		return nil
	}

	structs := make([]*qdrant.PointStruct, 0, len(points))
	for _, p := range points {
		payload := make(map[string]any, len(p.Payload)+1)
		for k, v := range p.Payload {
			payload[k] = v
		}
		payload[idField] = p.ID

		values, err := qdrant.TryValueMap(payload)
		if err != nil {
			return fmt.Errorf("encode payload for %s: %w", p.ID, err)
		}

		structs = append(structs, &qdrant.PointStruct{
			Id:      pointID(p.ID),
			Payload: values,
			Vectors: qdrant.NewVectorsMap(map[string]*qdrant.Vector{
				denseVector:  qdrant.NewVectorDense(p.Dense),
				sparseVector: qdrant.NewVectorSparse(p.Sparse.Indices, p.Sparse.Values),
			}),
		})
	}

	_, err := i.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: i.collection,
		Points:         structs,
		Wait:           qdrant.PtrOf(true),
	})
	return err
}

func (i *Index) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	pointIDs := make([]*qdrant.PointId, len(ids))
	for n, id := range ids {
		pointIDs[n] = pointID(id)
	}

	_, err := i.client.Delete(ctx, &qdrant.DeletePoints{
		CollectionName: i.collection,
		Points:         qdrant.NewPointsSelector(pointIDs...),
		Wait:           qdrant.PtrOf(true),
	})
	return err
}

func (i *Index) SetPayload(ctx context.Context, id string, payload map[string]any) error {
	values, err := qdrant.TryValueMap(payload)
	if err != nil {
		return fmt.Errorf("encode payload for %s: %w", id, err)
	}

	_, err = i.client.SetPayload(ctx, &qdrant.SetPayloadPoints{
		CollectionName: i.collection,
		Payload:        values,
		PointsSelector: qdrant.NewPointsSelector(pointID(id)),
		Wait:           qdrant.PtrOf(true),
	})
	return err
}

func (i *Index) Query(ctx context.Context, dense []float32, sparse search.SparseVector, filter *search.Filter, limit int) ([]search.Hit, error) {
	f := toFilter(filter)
	prefetchLimit := uint64(limit * prefetchFactor)

	prefetch := []*qdrant.PrefetchQuery{{
		Query:  qdrant.NewQueryDense(dense),
		Using:  qdrant.PtrOf(denseVector),
		Filter: f,
		Limit:  &prefetchLimit,
	}}
	if len(sparse.Indices) > 0 {
		prefetch = append(prefetch, &qdrant.PrefetchQuery{
			Query:  qdrant.NewQuerySparse(sparse.Indices, sparse.Values),
			Using:  qdrant.PtrOf(sparseVector),
			Filter: f,
			Limit:  &prefetchLimit,
		})
	}

	points, err := i.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: i.collection,
		Prefetch:       prefetch,
		Query:          qdrant.NewQueryFusion(qdrant.Fusion_RRF),
		Limit:          qdrant.PtrOf(uint64(limit)),
		WithPayload:    qdrant.NewWithPayload(true),
		WithVectors:    qdrant.NewWithVectorsInclude(denseVector),
	})
	if err != nil {
		return nil, err
	}

	hits := make([]search.Hit, 0, len(points))
	for _, p := range points {
		payload := fromPayload(p.GetPayload())
		hits = append(hits, search.Hit{
			ID:      popID(payload),
			Score:   float64(p.GetScore()),
			Payload: payload,
			Vector:  p.GetVectors().GetVectors().GetVectors()[denseVector].GetDenseVector().GetData(),
		})
	}
	return hits, nil
}

func (i *Index) Scroll(ctx context.Context, filter *search.Filter) ([]search.Hit, error) {
	iter := i.client.ScrollAll(ctx, &qdrant.ScrollPoints{
		CollectionName: i.collection,
		Filter:         toFilter(filter),
		WithPayload:    qdrant.NewWithPayload(true),
		Limit:          qdrant.PtrOf(uint32(256)),
	})

	var hits []search.Hit
	for {
		page, err := iter.Next()
		if errors.Is(err, io.EOF) {
			return hits, nil
		}
		if err != nil {
			return nil, err
		}
		for _, p := range page {
			payload := fromPayload(p.GetPayload())
			hits = append(hits, search.Hit{ID: popID(payload), Payload: payload})
		}
	}
}

func toFilter(f *search.Filter) *qdrant.Filter {
	if f == nil || len(f.Any) == 0 {
		return nil
	}
	return &qdrant.Filter{Must: []*qdrant.Condition{qdrant.NewMatchKeywords(f.Field, f.Any...)}}
}

func popID(payload map[string]any) string {
	id, _ := payload[idField].(string)
	delete(payload, idField)
	return id
}

func fromPayload(p map[string]*qdrant.Value) map[string]any {
	out := make(map[string]any, len(p))
	for k, v := range p {
		out[k] = fromValue(v)
	}
	return out
}

func fromValue(v *qdrant.Value) any {
	switch k := v.GetKind().(type) {
	case *qdrant.Value_StringValue:
		return k.StringValue
	case *qdrant.Value_IntegerValue:
		return k.IntegerValue
	case *qdrant.Value_DoubleValue:
		return k.DoubleValue
	case *qdrant.Value_BoolValue:
		return k.BoolValue
	case *qdrant.Value_ListValue:
		list := make([]any, 0, len(k.ListValue.GetValues()))
		for _, item := range k.ListValue.GetValues() {
			list = append(list, fromValue(item))
		}
		return list
	case *qdrant.Value_StructValue:
		return fromPayload(k.StructValue.GetFields())
	default:
		return nil
	}
}
