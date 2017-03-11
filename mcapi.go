package main

import (
	"encoding/json"
	"flag"
	"github.com/DeanThompson/ginpprof"
	"github.com/getsentry/raven-go"
	"github.com/gin-gonic/gin"
	"gopkg.in/redis.v5"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"
)

type Config struct {
	HttpAppHost  string
	RedisHost    string
	StaticFiles  string
	TemplateFile string
	SentryDSN    string
}

var redisClient *redis.Client

func loadConfig(path string) *Config {
	file, err := ioutil.ReadFile(path)

	if err != nil {
		raven.CaptureErrorAndWait(err, nil)
		panic(err)
	}

	var cfg Config
	json.Unmarshal(file, &cfg)

	return &cfg
}

func generateConfig(path string) {
	cfg := &Config{
		HttpAppHost:  ":8080",
		RedisHost:    ":6379",
		StaticFiles:  "./scripts",
		TemplateFile: "./templates/index.html",
	}

	data, err := json.MarshalIndent(cfg, "", "	")
	if err != nil {
		raven.CaptureErrorAndWait(err, nil)
	}

	err = ioutil.WriteFile(path, data, 0644)
	if err != nil {
		raven.CaptureErrorAndWait(err, nil)
	}
}

var fatalServerErrors []string = []string{
	"no such host",
	"no route",
	"unknown port",
	"too many colons in address",
	"invalid argument",
}

func updateServers() {
	servers, err := redisClient.SMembers("serverping").Result()
	if err != nil {
		raven.CaptureErrorAndWait(err, nil)
	}

	log.Printf("%d servers in ping database\n", len(servers))

	for _, server := range servers {
		go updatePing(server)
	}

	servers, err = redisClient.SMembers("serverquery").Result()
	if err != nil {
		raven.CaptureErrorAndWait(err, nil)
	}

	log.Printf("%d servers in query database\n", len(servers))

	for _, server := range servers {
		go updateQuery(server)
	}
}

func main() {
	configFile := flag.String("config", "config.json", "path to configuration file")
	genConfig := flag.Bool("gencfg", false, "generate configuration file with sane defaults")

	flag.Parse()

	f, _ := os.OpenFile("mcapi.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	defer f.Close()

	log.SetOutput(io.MultiWriter(f, os.Stdout))

	if *genConfig {
		generateConfig(*configFile)
		log.Println("Saved configuration file with sane defaults, please update as needed")
		os.Exit(0)
	}

	cfg := loadConfig(*configFile)

	raven.SetDSN(cfg.SentryDSN)

	redisClient = redis.NewClient(&redis.Options{
		Addr:     cfg.RedisHost,
		PoolSize: 1000,
	})

	go updateServers()
	go func() {
		t := time.NewTicker(time.Minute)

		for _ = range t.C {
			updateServers()
		}
	}()

	router := gin.New()
	router.Use(gin.Recovery())

	router.Static("/scripts", cfg.StaticFiles)
	router.LoadHTMLFiles(cfg.TemplateFile)

	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET")
		c.Writer.Header().Set("Cache-Control", "max-age=300, public, s-maxage=300")

		redisClient.Incr("mcapi")
	})

	router.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", gin.H{})
	})

	router.GET("/hi", func(c *gin.Context) {
		c.String(http.StatusOK, "Hello :3")
	})

	router.GET("/stats", func(c *gin.Context) {
		stats, err := redisClient.Get("mcapi").Int64()
		if err != nil {
			raven.CaptureErrorAndWait(err, nil)
		}

		c.JSON(http.StatusOK, gin.H{
			"stats": stats,
			"time":  time.Now().UnixNano(),
		})
	})

	router.GET("/server/status", respondServerStatus)
	router.GET("/minecraft/1.3/server/status", respondServerStatus)

	router.GET("/server/query", respondServerQuery)
	router.GET("/minecraft/1.3/server/query", respondServerQuery)

	ginpprof.Wrapper(router)

	router.Run(cfg.HttpAppHost)
}
