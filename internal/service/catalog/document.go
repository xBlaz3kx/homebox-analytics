package catalog

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/barcode"
	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/homebox"
	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/search"
)

//go:embed templates/*.tmpl
var templates embed.FS

var documentTmpl = template.Must(template.New("document.tmpl").
	Funcs(template.FuncMap{"join": strings.Join}).
	ParseFS(templates, "templates/document.tmpl"))

// Payload fields stored with each indexed item.
const (
	fieldItemID      = "item_id"
	fieldName        = "name"
	fieldTags        = "tags"
	fieldTagNames    = "tag_names"
	fieldLocation    = "location"
	fieldCodes       = "codes"
	fieldContentHash = "content_hash"
	fieldUpdatedAt   = "updated_at"
	fieldModel       = "embedding_model"
)

// KeywordFields are the payload fields that need a keyword index for filtering.
var KeywordFields = []string{fieldItemID, fieldCodes, fieldTags}

type documentData struct {
	Name         string
	Manufacturer string
	ModelNumber  string
	Description  string
	Tags         []string
}

// buildDocument renders the searchable text for an entity and its payload.
func buildDocument(e *homebox.Entity, codeFields []string, model string) (search.Document, error) {
	tagNames := make([]string, len(e.Tags))
	for i, t := range e.Tags {
		tagNames[i] = t.Name
	}

	var text bytes.Buffer
	err := documentTmpl.Execute(&text, documentData{
		Name:         e.Name,
		Manufacturer: e.Manufacturer,
		ModelNumber:  e.ModelNumber,
		Description:  e.Description,
		Tags:         tagNames,
	})
	if err != nil {
		return search.Document{}, err
	}

	codes := entityCodes(e, codeFields)
	location := ""
	if e.Location != nil {
		location = e.Location.Name
	}

	return search.Document{
		ID:   e.ID,
		Text: text.String(),
		Payload: map[string]any{
			fieldItemID:      e.ID,
			fieldName:        e.Name,
			fieldTags:        e.TagIDs(),
			fieldTagNames:    tagNames,
			fieldLocation:    location,
			fieldCodes:       codes,
			fieldContentHash: contentHash(text.String(), codes, e.TagIDs(), location, model),
			fieldUpdatedAt:   e.UpdatedAt.UTC().Format(time.RFC3339Nano),
			fieldModel:       model,
		},
	}, nil
}

// entityCodes collects normalized codes from configured custom fields, model/serial numbers and asset ID.
func entityCodes(e *homebox.Entity, codeFields []string) []string {
	var raw []string
	for _, f := range e.Fields {
		if slices.ContainsFunc(codeFields, func(name string) bool { return strings.EqualFold(name, f.Name) }) {
			// A field may hold several codes separated by commas or semicolons.
			raw = append(raw, strings.FieldsFunc(f.TextValue, func(r rune) bool { return r == ',' || r == ';' })...)
		}
	}
	raw = append(raw, e.ModelNumber, e.SerialNumber, e.AssetID)

	var codes []string
	for _, r := range raw {
		if c := barcode.Normalize(r); c.Value != "" && !slices.Contains(codes, c.Value) {
			codes = append(codes, c.Value)
		}
	}
	slices.Sort(codes)
	return codes
}

func contentHash(text string, codes, tags []string, location, model string) string {
	h := sha256.New()
	for _, part := range [][]string{{text}, codes, tags, {location, model}} {
		for _, s := range part {
			h.Write([]byte(s))
			h.Write([]byte{0})
		}
		h.Write([]byte{1})
	}
	return hex.EncodeToString(h.Sum(nil))
}
