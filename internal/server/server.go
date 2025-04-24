package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/rpc/v2"
	"github.com/gorilla/rpc/v2/json2"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Disdjj/mmp-server/internal/models"
)

// JSON-RPC error codes defined by MMP
const (
	ErrCodeNodeNotFound      = 10001
	ErrCodePathAlreadyExists = 10002 // GORM handles this via unique constraint violation
	ErrCodeTemplateNotFound  = 10003 // Not directly used here but defined
	ErrCodeInvalidTemplate   = 10004
	ErrCodeMemoryIDNotFound  = 10005
)

// Server holds the dependencies for the JSON-RPC server.
type Server struct {
	DB *gorm.DB // Use GORM DB instance
}

// NewServer creates a new server instance.
func NewServer(db *gorm.DB) *Server {
	s := &Server{
		DB: db,
	}
	return s
}

// Handle handles incoming JSON-RPC requests.
func (s *Server) Handle(w http.ResponseWriter, r *http.Request) {
	rpcServer := rpc.NewServer()
	rpcServer.RegisterCodec(json2.NewCodec(), "application/json")
	// Register the service methods under the correct prefixes
	rpcServer.RegisterService(s, "memory")
	rpcServer.RegisterService(s, "memManager")

	rpcServer.ServeHTTP(w, r)
}

// --- Utility Functions ---

// isNotFoundError checks if the error is GORM's record not found error.
func isGormNotFoundError(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}

// isDuplicateKeyError checks for unique constraint violation errors (PostgreSQL and SQLite).
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "unique constraint") || strings.Contains(errStr, "duplicate key value violates unique constraint") // PostgreSQL
}

func newRpcError(code json2.ErrorCode, message string) *json2.Error {
	return &json2.Error{Code: code, Message: message}
}

// --- memManager Methods ---

type CreateRequest struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Metadata    interface{} `json:"metadata,omitempty"`
}

type CreateResult struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Create implements memManager.create
func (s *Server) Create(r *http.Request, args *CreateRequest, result *CreateResult) error {
	ctx := r.Context()
	memoryID := "mm-" + uuid.NewString()

	var metadataJSON datatypes.JSON
	if args.Metadata != nil {
		metaBytes, err := json.Marshal(args.Metadata)
		if err != nil {
			log.Printf("Error marshaling metadata: %v", err)
			return newRpcError(json2.E_INVALID_REQ, "Invalid metadata format")
		}
		metadataJSON = datatypes.JSON(metaBytes)
	}

	newCollection := models.MemoryCollection{
		ID:          memoryID,
		Name:        args.Name,
		Description: args.Description,
		Metadata:    metadataJSON,
	}

	dbResult := s.DB.WithContext(ctx).Create(&newCollection)
	if dbResult.Error != nil {
		// GORM doesn't typically error on duplicate primary key during Create if it's generated,
		// but good practice to check, though UUID makes collision unlikely.
		if isDuplicateKeyError(dbResult.Error) {
			log.Printf("Error creating memory collection: ID %s already exists (UUID collision?)", memoryID)
			return newRpcError(json2.E_INTERNAL, "Failed to create memory collection due to ID conflict")
		}
		log.Printf("Error creating memory collection: %v", dbResult.Error)
		return newRpcError(json2.E_INTERNAL, "Failed to create memory collection")
	}

	*result = CreateResult{
		ID:          newCollection.ID,
		Name:        newCollection.Name,
		Description: newCollection.Description,
		CreatedAt:   newCollection.CreatedAt, // GORM automatically populates this
	}
	return nil
}

type TemplateNode struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path"`
	Attention   string `json:"attention,omitempty"`
	NeedInit    bool   `json:"needInit"`
	Format      string `json:"format,omitempty"`
	Type        string `json:"type"`
	Content     string `json:"content,omitempty"`
}

type ApplyTemplateRequest struct {
	MemoryID string         `json:"memoryId"`
	Template []TemplateNode `json:"template"`
}

type CreatedNodeInfo struct {
	Path     string `json:"path"`
	NeedInit bool   `json:"needInit"`
}

type ApplyTemplateResult struct {
	MemoryID     string            `json:"memoryId"`
	Success      bool              `json:"success"`
	CreatedNodes []CreatedNodeInfo `json:"createdNodes"`
}

// ApplyTemplate implements memManager.applyTemplate
func (s *Server) ApplyTemplate(r *http.Request, args *ApplyTemplateRequest, result *ApplyTemplateResult) error {
	ctx := r.Context()
	createdNodesInfo := make([]CreatedNodeInfo, 0, len(args.Template))
	success := true
	var validationError bool = false

	// 1. Check if Memory ID exists
	var collectionExists models.MemoryCollection
	if err := s.DB.WithContext(ctx).Select("id").First(&collectionExists, "id = ?", args.MemoryID).Error; err != nil {
		if isGormNotFoundError(err) {
			return newRpcError(ErrCodeMemoryIDNotFound, fmt.Sprintf("Memory ID %s not found", args.MemoryID))
		}
		log.Printf("Error checking memory collection %s: %v", args.MemoryID, err)
		return newRpcError(json2.E_INTERNAL, "Failed to check memory collection existence")
	}

	// 2. Process templates within a transaction
	err := s.DB.WithContext(ctx).Transaction(
		func(tx *gorm.DB) error {
			for _, nodeTpl := range args.Template {
				// Basic validation from OpenRPC spec
				if nodeTpl.NeedInit && nodeTpl.Content == "" {
					log.Printf("Invalid template node: needInit is true but content is empty for path %s in memory %s", nodeTpl.Path, args.MemoryID)
					success = false
					validationError = true
					continue // Skip this invalid node, but continue processing others in the transaction
				}

				nodeToUpsert := models.MemoryNode{
					MemoryID:    args.MemoryID,
					Path:        nodeTpl.Path,
					Name:        nodeTpl.Name,
					Description: nodeTpl.Description,
					Attention:   nodeTpl.Attention,
					NeedInit:    nodeTpl.NeedInit,
					Format:      nodeTpl.Format,
					Type:        nodeTpl.Type,
					Content:     nodeTpl.Content,
				}

				// GORM's Clauses(clause.OnConflict...) handles INSERT ON CONFLICT DO UPDATE
				// Note: This might require specific GORM tags or driver support to map `excluded` correctly.
				// A common approach is to specify the columns to update.
				dbResult := tx.Clauses(
					clause.OnConflict{
						Columns:   []clause.Column{{Name: "memory_id"}, {Name: "path"}},                                                             // Conflict target
						DoUpdates: clause.AssignmentColumns([]string{"name", "description", "attention", "need_init", "format", "type", "content"}), // Columns to update
					},
				).Create(&nodeToUpsert)

				if dbResult.Error != nil {
					log.Printf("Error upserting template node %s for memory %s: %v", nodeTpl.Path, args.MemoryID, dbResult.Error)
					success = false
					// Do not return error immediately, allow transaction to try others, but mark as failed.
					continue
				}

				// Fetch the potentially updated node to get correct NeedInit status after upsert
				var upsertedNode models.MemoryNode
				if err := tx.Select("path", "need_init").First(&upsertedNode, "memory_id = ? AND path = ?", args.MemoryID, nodeTpl.Path).Error; err != nil {
					log.Printf("Error fetching upserted node info %s for memory %s: %v", nodeTpl.Path, args.MemoryID, err)
					success = false
					continue
				}

				createdNodesInfo = append(
					createdNodesInfo, CreatedNodeInfo{
						Path:     upsertedNode.Path,
						NeedInit: upsertedNode.NeedInit, // Use the value from the DB after upsert
					},
				)
			}

			// If any node failed validation or DB operation, rollback the transaction
			if !success {
				return errors.New("one or more template nodes failed to apply") // This triggers rollback
			}

			return nil // Commit transaction if all nodes processed successfully
		},
	)

	// Check the final success status after the transaction attempt
	if err != nil {
		log.Printf("ApplyTemplate transaction failed for %s: %v", args.MemoryID, err)
		// Determine the specific error type if possible
		errorCode := json2.E_INTERNAL
		errMessage := "Failed to apply template nodes due to transaction error"
		if validationError {
			errorCode = ErrCodeInvalidTemplate
			errMessage = "One or more template nodes were invalid"
		}
		*result = ApplyTemplateResult{
			MemoryID:     args.MemoryID,
			Success:      false,
			CreatedNodes: createdNodesInfo, // May contain nodes processed before failure
		}
		return newRpcError(errorCode, errMessage)
	}

	*result = ApplyTemplateResult{
		MemoryID:     args.MemoryID,
		Success:      true,
		CreatedNodes: createdNodesInfo,
	}
	return nil
}

// --- memory Methods ---

type AddRequest struct {
	MemoryID string `json:"memoryId"`
	Node     struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Path        string `json:"path"`
		Attention   string `json:"attention,omitempty"`
		NeedInit    bool   `json:"needInit,omitempty"` // Default is false
		Format      string `json:"format,omitempty"`
		Type        string `json:"type"`
		Content     string `json:"content"` // Required by spec
	} `json:"node"`
}

type AddResult struct {
	Path string `json:"path"`
}

// Add implements memory.add (behaves like Upsert)
func (s *Server) Add(r *http.Request, args *AddRequest, result *AddResult) error {
	ctx := r.Context()

	// 1. Check if Memory ID exists
	var collectionExists models.MemoryCollection
	if err := s.DB.WithContext(ctx).Select("id").First(&collectionExists, "id = ?", args.MemoryID).Error; err != nil {
		if isGormNotFoundError(err) {
			return newRpcError(ErrCodeMemoryIDNotFound, fmt.Sprintf("Memory ID %s not found", args.MemoryID))
		}
		log.Printf("Error checking memory collection %s during Add: %v", args.MemoryID, err)
		return newRpcError(json2.E_INTERNAL, "Failed to check memory collection existence")
	}

	nodeToUpsert := models.MemoryNode{
		MemoryID:    args.MemoryID,
		Path:        args.Node.Path,
		Name:        args.Node.Name,
		Description: args.Node.Description,
		Attention:   args.Node.Attention,
		NeedInit:    args.Node.NeedInit,
		Format:      args.Node.Format,
		Type:        args.Node.Type,
		Content:     args.Node.Content, // Content is required for Add
	}

	// Use GORM's OnConflict clause for Upsert behavior
	dbResult := s.DB.WithContext(ctx).Clauses(
		clause.OnConflict{
			Columns:   []clause.Column{{Name: "memory_id"}, {Name: "path"}},
			DoUpdates: clause.AssignmentColumns([]string{"name", "description", "attention", "need_init", "format", "type", "content"}),
		},
	).Create(&nodeToUpsert)

	if dbResult.Error != nil {
		// Don't need to check for duplicate key, as OnConflict handles it.
		// Check for foreign key violation (MemoryID not found) - GORM might wrap this differently
		errStr := strings.ToLower(dbResult.Error.Error())
		if strings.Contains(errStr, "foreign key constraint") {
			log.Printf("Error adding node: Memory ID %s reference failed", args.MemoryID)
			// This case should ideally be caught by the initial check, but as a fallback:
			return newRpcError(ErrCodeMemoryIDNotFound, fmt.Sprintf("Memory ID %s not found (FK violation)", args.MemoryID))
		}

		log.Printf("Error adding/upserting memory node %s for memory %s: %v", args.Node.Path, args.MemoryID, dbResult.Error)
		return newRpcError(json2.E_INTERNAL, "Failed to add memory node")
	}

	*result = AddResult{Path: args.Node.Path} // Return the input path as GORM doesn't easily return values on upsert
	return nil
}

type GetRequest struct {
	MemoryID string `json:"memoryId"`
	Path     string `json:"path"`
}

// Get implements memory.get
// The result type uses the GORM model directly.
func (s *Server) Get(r *http.Request, args *GetRequest, result *models.MemoryNode) error {
	ctx := r.Context()
	var node models.MemoryNode

	dbResult := s.DB.WithContext(ctx).First(&node, "memory_id = ? AND path = ?", args.MemoryID, args.Path)

	if dbResult.Error != nil {
		if isGormNotFoundError(dbResult.Error) {
			return newRpcError(ErrCodeNodeNotFound, fmt.Sprintf("Node not found at path '%s' for memory ID '%s'", args.Path, args.MemoryID))
		}
		log.Printf("Error getting memory node %s for memory %s: %v", args.Path, args.MemoryID, dbResult.Error)
		return newRpcError(json2.E_INTERNAL, "Failed to get memory node")
	}

	*result = node // Directly assign the fetched node
	return nil
}

type GetInitNodesRequest struct {
	MemoryID string `json:"memoryId"`
	Filter   *struct {
		Path string `json:"path,omitempty"`
	} `json:"filter,omitempty"`
}

type GetInitNodesResultItem struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Attention   string `json:"attention,omitempty"`
}

// GetInitNodes implements memory.getInitNodes
func (s *Server) GetInitNodes(r *http.Request, args *GetInitNodesRequest, result *[]GetInitNodesResultItem) error {
	ctx := r.Context()
	var nodes []models.MemoryNode

	query := s.DB.WithContext(ctx).Select("path", "name", "description", "attention").Where("memory_id = ? AND need_init = ?", args.MemoryID, true)

	if args.Filter != nil && args.Filter.Path != "" {
		// Use LIKE for path prefix filtering
		query = query.Where("path LIKE ?", args.Filter.Path+"%")
	}

	if err := query.Find(&nodes).Error; err != nil {
		// GORM Find doesn't return ErrRecordNotFound for empty results, check foreign key violation maybe?
		// Best practice: check if memory ID exists first if strict error handling is needed.
		log.Printf("Error getting init nodes for memory %s: %v", args.MemoryID, err)
		return newRpcError(json2.E_INTERNAL, "Failed to get init nodes")
	}

	// Convert nodes to result format
	initNodesResult := make([]GetInitNodesResultItem, len(nodes))
	for i, n := range nodes {
		initNodesResult[i] = GetInitNodesResultItem{
			Path:        n.Path,
			Name:        n.Name,
			Description: n.Description,
			Attention:   n.Attention,
		}
	}

	*result = initNodesResult
	return nil
}

type ListRequest struct {
	MemoryID string `json:"memoryId"`
	Filter   *struct {
		Path     string `json:"path,omitempty"`
		Type     string `json:"type,omitempty"`
		NeedInit *bool  `json:"needInit,omitempty"` // Use pointer for optional boolean
	} `json:"filter,omitempty"`
	Pagination *struct {
		Offset int `json:"offset,omitempty"`
		Limit  int `json:"limit,omitempty"`
	} `json:"pagination,omitempty"`
}

type ListNodeInfo struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type ListResult struct {
	Total int64          `json:"total"` // Use int64 as GORM Count returns int64
	Nodes []ListNodeInfo `json:"nodes"`
}

// List implements memory.list
func (s *Server) List(r *http.Request, args *ListRequest, result *ListResult) error {
	ctx := r.Context()
	limit := 50 // Default limit
	offset := 0 // Default offset

	if args.Pagination != nil {
		if args.Pagination.Limit > 0 {
			limit = args.Pagination.Limit
		}
		if args.Pagination.Offset >= 0 { // Allow offset 0
			offset = args.Pagination.Offset
		}
	}

	// Base query for both count and list
	baseQuery := s.DB.WithContext(ctx).Model(&models.MemoryNode{}).Where("memory_id = ?", args.MemoryID)

	// Apply filters
	if args.Filter != nil {
		if args.Filter.Path != "" {
			baseQuery = baseQuery.Where("path LIKE ?", args.Filter.Path+"%")
		}
		if args.Filter.Type != "" {
			baseQuery = baseQuery.Where("type = ?", args.Filter.Type)
		}
		if args.Filter.NeedInit != nil {
			baseQuery = baseQuery.Where("need_init = ?", *args.Filter.NeedInit)
		}
	}

	// Get total count
	var totalCount int64
	if err := baseQuery.Count(&totalCount).Error; err != nil {
		// Could check for foreign key violation here too if needed.
		log.Printf("Error counting memory nodes for memory %s: %v", args.MemoryID, err)
		return newRpcError(json2.E_INTERNAL, "Failed to count memory nodes")
	}

	// Get paginated list
	var nodes []models.MemoryNode
	if err := baseQuery.Select("path", "name", "description").Order("path").Limit(limit).Offset(offset).Find(&nodes).Error; err != nil {
		log.Printf("Error listing memory nodes for memory %s: %v", args.MemoryID, err)
		return newRpcError(json2.E_INTERNAL, "Failed to list memory nodes")
	}

	// Convert nodes to result format
	listNodes := make([]ListNodeInfo, len(nodes))
	for i, n := range nodes {
		listNodes[i] = ListNodeInfo{
			Path:        n.Path,
			Name:        n.Name,
			Description: n.Description,
		}
	}

	*result = ListResult{
		Total: totalCount,
		Nodes: listNodes,
	}
	return nil
}

type UpdateRequest struct {
	MemoryID string `json:"memoryId"`
	Path     string `json:"path"`
	Updates  struct {
		Name        *string `json:"name,omitempty"`
		Description *string `json:"description,omitempty"`
		Content     *string `json:"content,omitempty"`
		Attention   *string `json:"attention,omitempty"`
		Format      *string `json:"format,omitempty"`
		NeedInit    *bool   `json:"needInit,omitempty"`
	} `json:"updates"`
}

type UpdateResult struct {
	Path      string    `json:"path"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Update implements memory.update
func (s *Server) Update(r *http.Request, args *UpdateRequest, result *UpdateResult) error {
	ctx := r.Context()

	// Check if node exists first (GORM update won't error if no rows match)
	var existingNode models.MemoryNode
	checkResult := s.DB.WithContext(ctx).Select("path").First(&existingNode, "memory_id = ? AND path = ?", args.MemoryID, args.Path)
	if checkResult.Error != nil {
		if isGormNotFoundError(checkResult.Error) {
			// Before declaring node not found, check if the MemoryID itself is valid
			var collectionExists models.MemoryCollection
			if err := s.DB.WithContext(ctx).Select("id").First(&collectionExists, "id = ?", args.MemoryID).Error; err != nil {
				if isGormNotFoundError(err) {
					return newRpcError(ErrCodeMemoryIDNotFound, fmt.Sprintf("Memory ID %s not found during update check", args.MemoryID))
				}
				log.Printf("Error checking memory collection %s during update: %v", args.MemoryID, err)
				return newRpcError(json2.E_INTERNAL, "Failed to check memory collection existence during update")
			}
			// If collection exists but node doesn't:
			return newRpcError(ErrCodeNodeNotFound, fmt.Sprintf("Node not found at path '%s' for memory ID '%s' during update", args.Path, args.MemoryID))
		} else {
			log.Printf("Error checking node existence before update %s for memory %s: %v", args.Path, args.MemoryID, checkResult.Error)
			return newRpcError(json2.E_INTERNAL, "Failed to check node existence before update")
		}
	}

	// Build map of updates to apply only non-nil fields
	updatesMap := make(map[string]interface{})
	if args.Updates.Name != nil {
		updatesMap["name"] = *args.Updates.Name
	}
	if args.Updates.Description != nil {
		updatesMap["description"] = sql.NullString{String: *args.Updates.Description, Valid: true}
	} else {
		// Allow explicitly setting description to null if needed
		// updatesMap["description"] = sql.NullString{Valid: false}
	}
	if args.Updates.Content != nil {
		updatesMap["content"] = sql.NullString{String: *args.Updates.Content, Valid: true}
	} else {
		// Allow explicitly setting content to null
		// updatesMap["content"] = sql.NullString{Valid: false}
	}
	if args.Updates.Attention != nil {
		updatesMap["attention"] = sql.NullString{String: *args.Updates.Attention, Valid: true}
	} else {
		// Allow explicitly setting attention to null
		// updatesMap["attention"] = sql.NullString{Valid: false}
	}
	if args.Updates.Format != nil {
		updatesMap["format"] = sql.NullString{String: *args.Updates.Format, Valid: true}
	} else {
		// Allow explicitly setting format to null
		// updatesMap["format"] = sql.NullString{Valid: false}
	}
	if args.Updates.NeedInit != nil {
		updatesMap["need_init"] = *args.Updates.NeedInit
	}

	// Ensure there are updates to apply
	if len(updatesMap) == 0 {
		// No fields to update, maybe return current state or specific message?
		// For now, fetch and return current state
		var currentNode models.MemoryNode
		if err := s.DB.WithContext(ctx).First(&currentNode, "memory_id = ? AND path = ?", args.MemoryID, args.Path).Error; err != nil {
			// Should not happen as we checked existence, but handle defensively
			log.Printf("Error fetching node after no-op update %s for memory %s: %v", args.Path, args.MemoryID, err)
			return newRpcError(json2.E_INTERNAL, "Failed to fetch node state after no-op update")
		}
		*result = UpdateResult{
			Path:      currentNode.Path,
			UpdatedAt: currentNode.UpdatedAt,
		}
		return nil
	}

	// Apply updates
	var updatedNode models.MemoryNode
	dbResult := s.DB.WithContext(ctx).Model(&updatedNode).Where("memory_id = ? AND path = ?", args.MemoryID, args.Path).Updates(updatesMap)

	if dbResult.Error != nil {
		log.Printf("Error updating memory node %s for memory %s: %v", args.Path, args.MemoryID, dbResult.Error)
		return newRpcError(json2.E_INTERNAL, "Failed to update memory node")
	}

	// Fetch the updated timestamp (GORM Updates doesn't reliably return the model)
	if err := s.DB.WithContext(ctx).Select("updated_at").First(&updatedNode, "memory_id = ? AND path = ?", args.MemoryID, args.Path).Error; err != nil {
		log.Printf("Error fetching updated timestamp for node %s, memory %s: %v", args.Path, args.MemoryID, err)
		return newRpcError(json2.E_INTERNAL, "Failed to fetch updated timestamp")
	}

	*result = UpdateResult{
		Path:      args.Path, // Path doesn't change
		UpdatedAt: updatedNode.UpdatedAt,
	}
	return nil
}

type DeleteRequest struct {
	MemoryID  string `json:"memoryId"`
	Path      string `json:"path"`
	Recursive *bool  `json:"recursive,omitempty"` // Default false
}

type DeleteResult struct {
	Success      bool `json:"success"`
	DeletedCount int  `json:"deletedCount"`
}

// Delete implements memory.delete
func (s *Server) Delete(r *http.Request, args *DeleteRequest, result *DeleteResult) error {
	ctx := r.Context()
	recursive := false
	if args.Recursive != nil {
		recursive = *args.Recursive
	}

	var dbResult *gorm.DB

	// Check if memory ID exists first, otherwise delete might silently do nothing
	var collectionExists models.MemoryCollection
	if err := s.DB.WithContext(ctx).Select("id").First(&collectionExists, "id = ?", args.MemoryID).Error; err != nil {
		if isGormNotFoundError(err) {
			return newRpcError(ErrCodeMemoryIDNotFound, fmt.Sprintf("Memory ID %s not found during delete", args.MemoryID))
		} else {
			log.Printf("Error checking memory collection %s during delete: %v", args.MemoryID, err)
			return newRpcError(json2.E_INTERNAL, "Failed to check memory collection existence during delete")
		}
	}

	if recursive {
		// Delete all nodes matching the path prefix
		dbResult = s.DB.WithContext(ctx).Where("memory_id = ? AND path LIKE ?", args.MemoryID, args.Path+"%").Delete(&models.MemoryNode{})
	} else {
		// Delete a single node
		dbResult = s.DB.WithContext(ctx).Where("memory_id = ? AND path = ?", args.MemoryID, args.Path).Delete(&models.MemoryNode{})
	}

	if dbResult.Error != nil {
		log.Printf("Error deleting memory node(s) at path %s (recursive: %t) for memory %s: %v", args.Path, recursive, args.MemoryID, dbResult.Error)
		return newRpcError(json2.E_INTERNAL, "Failed to delete memory node(s)")
	}

	deletedCount := int(dbResult.RowsAffected)
	*result = DeleteResult{
		Success:      deletedCount > 0,
		DeletedCount: deletedCount,
	}
	return nil
}

type BatchRequestItem struct {
	MemoryID string `json:"memoryId"`
	Path     string `json:"path"`
}

type BatchRequest struct {
	Requests []BatchRequestItem `json:"requests"`
}

// Batch implements memory.batch
// Result is []*interface{} containing either *models.MemoryNode or *json2.Error
func (s *Server) Batch(r *http.Request, args *BatchRequest, result *[]interface{}) error {
	ctx := r.Context()
	results := make([]interface{}, len(args.Requests))

	// Process each request individually (potential for optimization by grouping)
	for i, req := range args.Requests {
		var node models.MemoryNode
		dbResult := s.DB.WithContext(ctx).First(&node, "memory_id = ? AND path = ?", req.MemoryID, req.Path)

		if dbResult.Error != nil {
			if isGormNotFoundError(dbResult.Error) {
				// Before declaring node not found, check if the MemoryID is valid
				var collectionExists models.MemoryCollection
				if err := s.DB.WithContext(ctx).Select("id").First(&collectionExists, "id = ?", req.MemoryID).Error; err != nil {
					if isGormNotFoundError(err) {
						results[i] = newRpcError(ErrCodeMemoryIDNotFound, fmt.Sprintf("Memory ID %s not found during batch get", req.MemoryID))
					} else {
						log.Printf("Error checking memory collection %s during batch get: %v", req.MemoryID, err)
						results[i] = newRpcError(json2.E_INTERNAL, fmt.Sprintf("Failed to check collection existence for %s", req.MemoryID))
					}
				} else {
					// Collection exists, but node doesn't
					results[i] = newRpcError(ErrCodeNodeNotFound, fmt.Sprintf("Node not found at path '%s' for memory ID '%s'", req.Path, req.MemoryID))
				}
			} else {
				log.Printf("Error getting batch memory node %s for memory %s: %v", req.Path, req.MemoryID, dbResult.Error)
				results[i] = newRpcError(json2.E_INTERNAL, fmt.Sprintf("Failed to get node %s for memory %s", req.Path, req.MemoryID))
			}
		} else {
			// Return the GORM model directly, relying on its json tags
			// Create a copy to avoid pointer issues if the slice is held onto
			nodeCopy := node
			results[i] = &nodeCopy
		}
	}

	*result = results
	return nil
}

// ServeStaticFiles sets up handlers for serving static files from the static directory
func (s *Server) ServeStaticFiles(mux *http.ServeMux) {
	fs := http.FileServer(http.Dir("./static"))
	mux.Handle("/", fs)
}

// GetAllCollectionsResult 包含所有集合的列表
type GetAllCollectionsResult struct {
	Collections []models.MemoryCollection `json:"collections"`
}

// GetAllCollections 实现 memory.GetAllCollections
func (s *Server) GetAllCollections(r *http.Request, args *struct{}, result *GetAllCollectionsResult) error {
	ctx := r.Context()
	var collections []models.MemoryCollection

	dbResult := s.DB.WithContext(ctx).Find(&collections)
	if dbResult.Error != nil {
		log.Printf("获取所有记忆集合时出错: %v", dbResult.Error)
		return newRpcError(json2.E_INTERNAL, "获取记忆集合列表失败")
	}

	*result = GetAllCollectionsResult{
		Collections: collections,
	}
	return nil
}
