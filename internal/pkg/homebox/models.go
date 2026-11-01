package homebox

import "time"

type Tag struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ParentID    string `json:"parentId"`
}

type EntityRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type EntitySummary struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	AssetID     string    `json:"assetId"`
	Quantity    float64   `json:"quantity"`
	Archived    bool      `json:"archived"`
	Tags        []Tag     `json:"tags"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Field struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	TextValue string `json:"textValue"`
}

type Entity struct {
	EntitySummary
	Manufacturer string     `json:"manufacturer"`
	ModelNumber  string     `json:"modelNumber"`
	SerialNumber string     `json:"serialNumber"`
	PurchaseFrom string     `json:"purchaseFrom"`
	Notes        string     `json:"notes"`
	Location     *EntityRef `json:"location"`
	Fields       []Field    `json:"fields"`
}

type EntityPage struct {
	Items    []EntitySummary `json:"items"`
	Page     int             `json:"page"`
	PageSize int             `json:"pageSize"`
	Total    int             `json:"total"`
}

// TagIDs returns the IDs of the entity's tags.
func (e EntitySummary) TagIDs() []string {
	ids := make([]string, len(e.Tags))
	for i, t := range e.Tags {
		ids[i] = t.ID
	}
	return ids
}
