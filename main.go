package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Disdjj/mmp-server/internal/models"
	"github.com/Disdjj/mmp-server/internal/server"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "18080"
	}

	dbDriver := os.Getenv("DB_DRIVER")
	if dbDriver == "" {
		dbDriver = "sqlite" // 默认使用sqlite
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		if dbDriver == "sqlite" {
			dbURL = "mmp.db" // Default SQLite file name
		} else {
			log.Fatal("DATABASE_URL environment variable is not set for non-sqlite driver")
		}
	}

	db, err := initializeDatabase(dbDriver, dbURL)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Auto-migrate the schema
	log.Println("Running database migrations...")
	if err := db.AutoMigrate(&models.MemoryCollection{}, &models.MemoryNode{}); err != nil {
		log.Fatalf("Failed to auto-migrate database: %v", err)
	}
	log.Println("Database migrations completed.")

	srv := server.NewServer(db)

	// 创建HTTP路由复用器
	mux := http.NewServeMux()

	// 设置RPC处理程序
	mux.HandleFunc("/rpc", srv.Handle)

	// 设置静态文件服务
	srv.ServeStaticFiles(mux)

	log.Printf("Server starting on port %s using %s driver", port, dbDriver)
	log.Printf("静态HTML文件已挂载在根路径，访问 http://localhost:%s/", port)

	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func initializeDatabase(driver, url string) (*gorm.DB, error) {
	var dialector gorm.Dialector

	switch driver {
	case "postgres":
		dialector = postgres.Open(url)
	case "sqlite":
		dialector = sqlite.Open(url) // e.g., "mmp.db"
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", driver)
	}

	// Configure GORM logger
	newLogger := logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags), // io writer
		logger.Config{
			SlowThreshold:             time.Second, // Slow SQL threshold
			LogLevel:                  logger.Info, // Log level (Silent, Error, Warn, Info)
			IgnoreRecordNotFoundError: true,        // Don't log ErrRecordNotFound
			Colorful:                  true,        // Disable color
		},
	)

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: newLogger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect database using %s driver: %w", driver, err)
	}

	log.Printf("Database connection established using %s driver", driver)
	return db, nil
}
