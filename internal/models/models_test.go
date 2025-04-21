package models

import (
	"encoding/json"
	"testing"
)

func TestMemoryCollectionMetadata(t *testing.T) {
	// 创建一个测试集合
	testCollection := MemoryCollection{
		ID:          "test-id",
		Name:        "测试集合",
		Description: "这是一个测试集合",
	}

	// 测试空元数据
	data, err := testCollection.GetMetadataMap()
	if err != nil {
		t.Errorf("GetMetadataMap应该对空元数据返回nil错误，但得到：%v", err)
	}
	if data != nil {
		t.Errorf("空元数据应该返回nil，但得到：%v", data)
	}

	// 测试设置元数据
	testData := map[string]interface{}{
		"key1": "value1",
		"key2": 123,
		"key3": true,
	}
	err = testCollection.SetMetadataMap(testData)
	if err != nil {
		t.Errorf("SetMetadataMap应该成功，但得到错误：%v", err)
	}

	// 测试获取设置的元数据
	retrievedData, err := testCollection.GetMetadataMap()
	if err != nil {
		t.Errorf("GetMetadataMap应该成功，但得到错误：%v", err)
	}
	if retrievedData == nil {
		t.Error("元数据不应为nil")
	}

	// 验证键值对是否正确
	if retrievedData["key1"] != "value1" {
		t.Errorf("key1应为value1，但得到：%v", retrievedData["key1"])
	}
	if num, ok := retrievedData["key2"].(float64); !ok || num != 123 {
		t.Errorf("key2应为123，但得到：%v", retrievedData["key2"])
	}
	if retrievedData["key3"] != true {
		t.Errorf("key3应为true，但得到：%v", retrievedData["key3"])
	}

	// 测试JSON序列化和反序列化
	jsonBytes, err := json.Marshal(testCollection)
	if err != nil {
		t.Errorf("JSON序列化失败：%v", err)
	}

	var deserializedCollection MemoryCollection
	err = json.Unmarshal(jsonBytes, &deserializedCollection)
	if err != nil {
		t.Errorf("JSON反序列化失败：%v", err)
	}

	// 验证反序列化后的对象
	if deserializedCollection.ID != testCollection.ID {
		t.Errorf("反序列化后ID不匹配：expected %s, got %s", testCollection.ID, deserializedCollection.ID)
	}
	if deserializedCollection.Name != testCollection.Name {
		t.Errorf("反序列化后Name不匹配：expected %s, got %s", testCollection.Name, deserializedCollection.Name)
	}
}
