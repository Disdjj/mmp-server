package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Disdjj/mmp-server/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 测试辅助函数，创建临时的记忆数据库
func setupTestDB(t *testing.T) *gorm.DB {
	// 使用SQLite记忆数据库进行测试
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("设置测试数据库失败: %v", err)
	}

	// 迁移模型
	err = db.AutoMigrate(&models.MemoryCollection{}, &models.MemoryNode{})
	if err != nil {
		t.Fatalf("迁移模型失败: %v", err)
	}

	return db
}

// 测试RPC调用的辅助函数
func callRPC(t *testing.T, server *Server, method string, params interface{}, result interface{}) *httptest.ResponseRecorder {
	// 创建RPC请求
	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  []interface{}{params},
		"id":      1,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("序列化请求失败: %v", err)
	}

	// 创建HTTP请求
	req, err := http.NewRequest("POST", "/rpc", bytes.NewBuffer(jsonData))
	if err != nil {
		t.Fatalf("创建HTTP请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// 记录响应
	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(server.Handle)
	handler.ServeHTTP(rr, req)

	// 如果提供了result，则解析响应
	if result != nil && rr.Code == http.StatusOK {
		var rpcResponse struct {
			Result json.RawMessage `json:"result"`
		}

		err = json.Unmarshal(rr.Body.Bytes(), &rpcResponse)
		if err != nil {
			t.Fatalf("解析RPC响应失败: %v", err)
		}

		err = json.Unmarshal(rpcResponse.Result, result)
		if err != nil {
			t.Fatalf("解析结果失败: %v", err)
		}
	}

	return rr
}

// 基本的服务器创建测试
func TestNewServer(t *testing.T) {
	db := setupTestDB(t)
	srv := NewServer(db)

	if srv == nil {
		t.Fatal("NewServer应该返回非nil的服务器实例")
	}

	if srv.DB != db {
		t.Error("服务器应该使用提供的数据库连接")
	}
}

// 测试清理函数，删除测试数据库
func TestMain(m *testing.M) {
	// 运行测试
	code := m.Run()

	// 清理（如果需要）

	// 退出
	os.Exit(code)
}
