package models

import (
	"database/sql"
	"encoding/json"
	"time"

	"gorm.io/datatypes"
)

// MemoryCollection corresponds to the memory_collections table
type MemoryCollection struct {
	ID          string         `gorm:"type:varchar(40);primaryKey" json:"id"`
	Name        string         `gorm:"type:varchar(255);not null" json:"name"`
	Description string         `gorm:"type:text;not null" json:"description"`
	Metadata    datatypes.JSON `gorm:"type:jsonb" json:"metadata,omitempty"` // Use GORM's JSON type
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`

	// Association (optional but can be useful)
	MemoryNodes []MemoryNode `gorm:"foreignKey:MemoryID;constraint:OnDelete:CASCADE;"`
}

// MemoryNode corresponds to the memory_nodes table
type MemoryNode struct {
	MemoryID    string         `gorm:"type:varchar(40);primaryKey" json:"memoryId"`
	Path        string         `gorm:"type:text;primaryKey" json:"path"`
	Name        string         `gorm:"type:varchar(255);not null" json:"name"`
	Description sql.NullString `gorm:"type:text" json:"description,omitempty"`
	Attention   sql.NullString `gorm:"type:text" json:"attention,omitempty"`
	NeedInit    bool           `gorm:"not null;default:false" json:"needInit"`
	Format      sql.NullString `gorm:"type:varchar(50)" json:"format,omitempty"`
	Type        string         `gorm:"type:varchar(20);not null;check:type IN ('json', 'markdown', 'xml', 'plaintext')" json:"type"`
	Content     sql.NullString `gorm:"type:text" json:"content,omitempty"` // Allow null content, needInit determines requirement
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}

// Helper methods for JSON metadata (if needed outside GORM)
func (mc *MemoryCollection) GetMetadataMap() (map[string]interface{}, error) {
	if mc.Metadata == nil || len(mc.Metadata) == 0 || string(mc.Metadata) == "null" {
		return nil, nil
	}
	var data map[string]interface{}
	err := json.Unmarshal(mc.Metadata, &data)
	return data, err
}

func (mc *MemoryCollection) SetMetadataMap(data map[string]interface{}) error {
	if data == nil {
		mc.Metadata = nil
		return nil
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	mc.Metadata = datatypes.JSON(b)
	return nil
}
