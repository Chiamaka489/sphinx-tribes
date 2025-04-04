package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/robfig/cron"
	"github.com/stakwork/sphinx-tribes/auth"
	"github.com/stakwork/sphinx-tribes/config"
	"github.com/stakwork/sphinx-tribes/db"
	_ "github.com/stakwork/sphinx-tribes/docs"
	"github.com/stakwork/sphinx-tribes/handlers"
	"github.com/stakwork/sphinx-tribes/routes"
	"github.com/stakwork/sphinx-tribes/websocket"
	"gopkg.in/go-playground/validator.v9"
)

// @title						Sphinx Tribes API
// @version					1.0
// @description				This is the API documentation for Sphinx Tribes.
// @host						localhost:5002
// @BasePath					/
//
// @SecurityDefinitions.apiKey	PubKeyContextAuth
// @In							header
// @Name						x-jwt
// @Description				JWT token for authentication. Can also be provided as a query parameter named 'token'
//
// @SecurityDefinitions.apiKey	SuperAdminAuth
// @In							header
// @Name						x-jwt
// @Description				JWT token for super admin authentication. Can also be provided as a query parameter named 'token'
//
// @SecurityDefinitions.apikey	CypressAuth
// @In							header
// @Name						x-cypress
// @Description				No authentication needed when in test mode
func main() {
	if err := godotenv.Load(); err != nil {
		fmt.Println("no .env file")
	}

	db.InitDB()
	db.InitRedis()
	db.InitCache()
	db.InitRoles()
	db.DB.ProcessUpdateTicketsWithoutGroup()

	// Config has to be inited before JWT, if not it will lead to NO JWT error
	config.InitConfig()
	auth.InitJwt()

	// validate
	db.Validate = validator.New()
	// Start websocket pool
	go websocket.WebsocketPool.Start()

	skipLoops := os.Getenv("SKIP_LOOPS")
	if skipLoops != "true" {
		go handlers.ProcessTwitterConfirmationsLoop()
		go handlers.ProcessGithubIssuesLoop()
	}

	runCron()
	run()
}

func runCron() {
	c := cron.New()
	c.AddFunc("@every 0h30m0s", handlers.InitV2PaymentsCron)
	c.AddFunc("@every 0h0m30s", handlers.ProcessWaitingNotifications)
	c.Start()
}

// Start the MQTT plugin
func run() {
	router := routes.NewRouter()

	shutdownSignal := make(chan os.Signal)
	signal.Notify(shutdownSignal, syscall.SIGINT, syscall.SIGTERM)
	<-shutdownSignal

	// shutdown web server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := router.Shutdown(ctx); err != nil {
		fmt.Printf("error shutting down server: %s", err.Error())
	}
}
