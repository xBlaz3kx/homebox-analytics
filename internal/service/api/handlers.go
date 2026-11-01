package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/xBlaz3kx/homebox-analytics/internal/service/analytics"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/catalog"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/ledger"
)

func queryInt(c *gin.Context, name string) (int, error) {
	v := c.Query(name)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, &badRequest{name + " must be a non-negative integer"}
	}
	return n, nil
}

func queryTime(c *gin.Context, name string) (time.Time, error) {
	v := c.Query(name)
	if v == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339, time.DateOnly} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, &badRequest{name + " must be RFC3339 or YYYY-MM-DD"}
}

// queryList accepts both repeated params (?items=a&items=b) and comma-separated values.
func queryList(c *gin.Context, name string) []string {
	var out []string
	for _, v := range c.QueryArray(name) {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func (h *Handler) lookup(c *gin.Context) {
	limit, err := queryInt(c, "limit")
	if err != nil {
		h.writeError(c, err)
		return
	}

	res, err := h.catalog.Resolve(c.Request.Context(), catalog.LookupQuery{
		Code: c.Query("code"), Vendor: c.Query("vendor"), Text: c.Query("q"), Limit: limit,
	})
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *Handler) listCodes(c *gin.Context) {
	links, err := h.catalog.ListCodes(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, links)
}

type linkCodeRequest struct {
	Code   string             `json:"code"`
	Vendor string             `json:"vendor"`
	ItemID string             `json:"item_id"`
	Source catalog.CodeSource `json:"source"`
}

func (h *Handler) linkCode(c *gin.Context) {
	var req linkCodeRequest
	if err := decode(c, &req); err != nil {
		h.writeError(c, err)
		return
	}
	if req.Code == "" || req.ItemID == "" {
		h.writeError(c, &badRequest{"code and item_id are required"})
		return
	}
	if req.Source == "" {
		req.Source = catalog.SourceManual
	}

	link, err := h.catalog.LinkCode(c.Request.Context(), req.Code, req.Vendor, req.ItemID, req.Source)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, link)
}

func (h *Handler) deleteCode(c *gin.Context) {
	if err := h.catalog.DeleteCode(c.Request.Context(), c.Param("id")); err != nil {
		h.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) recordEvent(c *gin.Context) {
	var in ledger.EventInput
	if err := decode(c, &in); err != nil {
		h.writeError(c, err)
		return
	}
	in.ItemID = c.Param("id")

	ev, err := h.ledger.RecordEvent(c.Request.Context(), in)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, ev)
}

func (h *Handler) listEvents(c *gin.Context) {
	from, err := queryTime(c, "from")
	if err != nil {
		h.writeError(c, err)
		return
	}
	to, err := queryTime(c, "to")
	if err != nil {
		h.writeError(c, err)
		return
	}
	limit, err := queryInt(c, "limit")
	if err != nil {
		h.writeError(c, err)
		return
	}

	events, err := h.ledger.ListEvents(c.Request.Context(), ledger.EventFilter{ItemID: c.Param("id"), From: from, To: to, Limit: limit})
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, events)
}

type scanRequest struct {
	Code   string `json:"code"`
	Vendor string `json:"vendor"`
	Text   string `json:"text"`
	// Learn links the code to the matched item when the match was hybrid but confident.
	Learn bool `json:"learn"`
	ledger.EventInput
}

type scanResponse struct {
	Match catalog.LookupResult `json:"match"`
	Event *ledger.Event        `json:"event,omitempty"`
	Link  *catalog.CodeLink    `json:"link,omitempty"`
}

// scan resolves a code/text and records the event in one call. Without a confident match it
// returns 409 with the candidates so the client can pick one and use /items/{id}/events.
func (h *Handler) scan(c *gin.Context) {
	var req scanRequest
	if err := decode(c, &req); err != nil {
		h.writeError(c, err)
		return
	}

	match, err := h.catalog.Resolve(c.Request.Context(), catalog.LookupQuery{Code: req.Code, Vendor: req.Vendor, Text: req.Text})
	if err != nil {
		h.writeError(c, err)
		return
	}
	best, ok := match.Best()
	if !ok {
		c.JSON(http.StatusConflict, errorBody{Error: "no confident match", Data: scanResponse{Match: match}})
		return
	}

	in := req.EventInput
	in.ItemID = best.ItemID
	if in.CodeUsed == "" {
		in.CodeUsed = req.Code
	}
	if in.Vendor == "" {
		in.Vendor = req.Vendor
	}

	ev, err := h.ledger.RecordEvent(c.Request.Context(), in)
	if err != nil {
		h.writeError(c, err)
		return
	}
	resp := scanResponse{Match: match, Event: ev}

	if req.Learn && req.Code != "" && match.MatchType == catalog.MatchHybrid {
		link, err := h.catalog.LinkCode(c.Request.Context(), req.Code, req.Vendor, best.ItemID, catalog.SourceLearned)
		if err != nil {
			h.writeError(c, err)
			return
		}
		resp.Link = link
	}
	c.JSON(http.StatusCreated, resp)
}

func (h *Handler) reconcile(c *gin.Context) {
	var in ledger.ReconcileInput
	if err := decode(c, &in); err != nil {
		h.writeError(c, err)
		return
	}
	in.ItemID = c.Param("id")

	rec, err := h.ledger.Reconcile(c.Request.Context(), in)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, rec)
}

type reconcileBatchRequest struct {
	Items []ledger.ReconcileInput `json:"items"`
}

func (h *Handler) reconcileBatch(c *gin.Context) {
	var req reconcileBatchRequest
	if err := decode(c, &req); err != nil {
		h.writeError(c, err)
		return
	}
	if len(req.Items) == 0 {
		h.writeError(c, &badRequest{"items must not be empty"})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"results": h.ledger.ReconcileBatch(c.Request.Context(), req.Items)})
}

func (h *Handler) listReconciliations(c *gin.Context) {
	limit, err := queryInt(c, "limit")
	if err != nil {
		h.writeError(c, err)
		return
	}
	recs, err := h.ledger.ListReconciliations(c.Request.Context(), c.Query("item_id"), limit)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, recs)
}

func (h *Handler) chartQuery(c *gin.Context) {
	from, err := queryTime(c, "from")
	if err != nil {
		h.writeError(c, err)
		return
	}
	to, err := queryTime(c, "to")
	if err != nil {
		h.writeError(c, err)
		return
	}

	h.chart(c, analytics.Query{
		Items:      queryList(c, "items"),
		Categories: queryList(c, "categories"),
		GroupBy:    analytics.GroupBy(c.Query("group_by")),
		Metric:     analytics.Metric(c.Query("metric")),
		Interval:   analytics.Interval(c.Query("interval")),
		From:       from,
		To:         to,
		Timezone:   c.Query("timezone"),
	})
}

func (h *Handler) chartBody(c *gin.Context) {
	var q analytics.Query
	if err := decode(c, &q); err != nil {
		h.writeError(c, err)
		return
	}
	h.chart(c, q)
}

func (h *Handler) chart(c *gin.Context, q analytics.Query) {
	chart, err := h.analytics.Series(c.Request.Context(), q)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, chart)
}

func (h *Handler) sync(c *gin.Context) {
	stats, err := h.catalog.Sync(c.Request.Context())
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, stats)
}
