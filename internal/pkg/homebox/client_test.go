package homebox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientLoginAndPaging(t *testing.T) {
	var logins atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/users/login", func(w http.ResponseWriter, r *http.Request) {
		logins.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "Bearer tok", "expiresAt": time.Now().Add(time.Hour)})
	})
	mux.HandleFunc("GET /api/v1/entities", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		assert.Equal(t, []string{"t1"}, r.URL.Query()["tags"])

		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		items := []EntitySummary{}
		// 150 items total: page 1 full, page 2 partial.
		n := 100
		if page == 2 {
			n = 50
		}
		for i := range n {
			items = append(items, EntitySummary{ID: fmt.Sprintf("%d-%d", page, i)})
		}
		_ = json.NewEncoder(w).Encode(EntityPage{Items: items, Page: page, Total: 150})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(Config{URL: srv.URL, Username: "u", Password: "p"})
	require.NoError(t, err)

	all, err := c.ListAllEntities(context.Background(), []string{"t1"})
	require.NoError(t, err)
	assert.Len(t, all, 150)
	assert.Equal(t, int32(1), logins.Load(), "token should be cached across pages")
}

func TestClientSetQuantityWithAPIKey(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/entities/{id}", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer key", r.Header.Get("Authorization"))

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, r.PathValue("id"), body["id"])
		assert.InDelta(t, 3.0, body["quantity"], 0)

		_ = json.NewEncoder(w).Encode(Entity{EntitySummary: EntitySummary{ID: r.PathValue("id"), Quantity: 3}})
	})
	mux.HandleFunc("GET /api/v1/entities/{id}", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(Config{URL: srv.URL, APIKey: "key"})
	require.NoError(t, err)

	e, err := c.SetQuantity(context.Background(), "abc", 3)
	require.NoError(t, err)
	assert.Equal(t, 3.0, e.Quantity)

	_, err = c.GetEntity(context.Background(), "missing")
	assert.ErrorIs(t, err, ErrNotFound)
}
