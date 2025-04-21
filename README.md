# MMP Server (Go + GORM + PostgreSQL/SQLite)

这是一个基于 Go 实现的 Model-Memory-Protocol (MMP) 服务器，使用 GORM 作为 ORM，支持 PostgreSQL 和 SQLite 作为数据存储。

## 特性

*   实现了 `mmp-openrpc.json` 中定义的所有方法。
*   基于 JSON-RPC 2.0 协议。
*   使用 GORM 进行数据库交互，支持 PostgreSQL 和 SQLite。
*   GORM 自动迁移数据库结构。
*   提供 Docker 和 Docker Compose 配置，方便部署和运行。
## 技术栈

*   Go 1.24+
*   GORM (ORM)
*   PostgreSQL 15+ (默认)
*   SQLite
*   gorilla/rpc/v2 (JSON-RPC library)
*   Docker

## 项目结构

```
.
├── cmd/mmp-server/     # 主程序入口
│   └── main.go
├── internal/
│   ├── models/         # GORM 模型定义
│   │   └── models.go
│   └── server/         # JSON-RPC 服务器逻辑和方法实现
│       └── server.go
├── .gitignore
├── Dockerfile            # Go 应用的 Docker 构建文件
├── docker-compose.yml    # Docker Compose 配置文件
├── go.mod
├── go.sum
├── mmp-openrpc.json      # MMP 协议定义
└── README.md             # 本文档
```

## 运行

确保你已经安装了 Docker 和 Docker Compose。

**默认使用 PostgreSQL:**

1.  **构建并启动服务:**

    ```bash
    docker-compose up --build -d
    ```

    这将会在后台构建 Go 应用镜像，启动 PostgreSQL 数据库容器，并运行 MMP 服务器。GORM 会自动创建所需的表。

2.  **访问服务:**

    服务将在本地的 `18080` 端口上监听 JSON-RPC 请求。你可以使用 `curl` 或其他工具向 `http://localhost:18080/rpc` 发送 POST 请求。
    (示例请求见下文)

3.  **查看日志:**

    ```bash
    docker-compose logs -f app
    docker-compose logs -f db
    ```

4.  **停止服务:**

    ```bash
    docker-compose down
    ```
    删除数据库数据卷：`docker-compose down -v`

**使用 SQLite:**

1.  **创建 `.env` 文件** (可选，也可以直接设置环境变量):
    在项目根目录创建 `.env` 文件，内容如下:
    ```dotenv
    DB_DRIVER=sqlite
    DATABASE_URL=./data/mmp.db # 数据库文件将存储在容器内的 /app/data/mmp.db
    ```

2.  **修改 `docker-compose.yml`** (如果使用 SQLite 文件存储):
    取消 `app` 服务下 `volumes` 的注释，将本地 `./data` 目录映射到容器内的 `/app/data`，以持久化 SQLite 文件。
    ```yaml
    # ...
    app:
      # ...
      volumes:
        - ./data:/app/data # Mount local ./data to /app/data in container
    ```
    确保本地存在 `data` 目录: `mkdir data`

3.  **构建并启动服务:**

    ```bash
    # 如果创建了 .env 文件，Docker Compose 会自动加载
    docker-compose up --build -d
    ```
    或者直接设置环境变量启动:
    ```bash
    DB_DRIVER=sqlite DATABASE_URL=./data/mmp.db docker-compose up --build -d
    ```
    这将启动 `app` 服务（不启动 `db` 服务），并将 SQLite 数据库文件存储在映射的卷中。

4.  **访问服务:** (同上)

5.  **查看日志:**
    ```bash
    docker-compose logs -f app
    ```

6.  **停止服务:**
    ```bash
    docker-compose down
    ```
    如果使用了卷映射，SQLite 文件将保留在本地的 `data` 目录下。

## 示例 JSON-RPC 请求

**获取所有集合:**

```bash
curl -X POST http://localhost:18080/rpc \
     -H 'Content-Type: application/json' \
     -d '{
          "jsonrpc": "2.0",
          "method": "memory.GetAllCollections",
          "params": [{}],
          "id": 1
        }'
```

**创建 Memory Collection:**

```bash
curl -X POST http://localhost:18080/rpc \
     -H 'Content-Type: application/json' \
     -d '{
          "jsonrpc": "2.0",
          "method": "memManager.Create",
          "params": [{
            "name": "MyGormMemory",
            "description": "A memory collection managed by GORM",
            "metadata": {"source": "example"}
          }],
          "id": 2
        }'
```

**添加 Memory Node:** (假设上面返回的 id 是 "mm-...")

```bash
curl -X POST http://localhost:18080/rpc \
     -H 'Content-Type: application/json' \
     -d '{
          "jsonrpc": "2.0",
          "method": "memory.Add",
          "params": [{
            "memoryId": "mm-...",
            "node": {
              "path": "/info/status",
              "name": "System Status",
              "type": "json",
              "content": "{\"online\": true, \"version\": \"1.0\"}"
            }
          }],
          "id": 3
        }'
```

**获取节点列表:**

```bash
curl -X POST http://localhost:18080/rpc \
     -H 'Content-Type: application/json' \
     -d '{
          "jsonrpc": "2.0",
          "method": "memory.List",
          "params": [{
            "memoryId": "mm-...",
            "filter": {}
          }],
          "id": 4
        }'
```

## 开发

*   **模型修改:** 编辑 `internal/models/models.go` 文件。
GORM 的 `AutoMigrate` 会在下次启动时尝试更新数据库结构（注意：它不会删除列或索引）。
*   **服务逻辑:** 编辑 `internal/server/server.go`。
*   **本地运行:**
    *   设置环境变量 `DB_DRIVER` (e.g., `postgres` or `sqlite`)。
    *   设置 `DATABASE_URL`：
        *   PostgreSQL: `export DATABASE_URL="host=localhost port=5432 user=user password=password dbname=mmp_db sslmode=disable"` (需要本地运行 PostgreSQL)
        *   SQLite: `export DATABASE_URL="local_mmp.db"`
    *   运行: `go run ./cmd/mmp-server/main.go`

## Web 界面

服务器包含一个简单的 Web 界面，可用于管理记忆集合和节点。启动服务器后，可通过浏览器访问 `http://localhost:18080/` 使用该界面。

主要功能包括：
* 查看所有记忆集合列表
* 创建和删除记忆集合
* 查看、创建、编辑和删除节点
* 支持手动输入 memoryID 访问任意节点，无需先从下拉菜单选择