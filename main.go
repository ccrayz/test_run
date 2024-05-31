package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
)

var (
	logDirectory = "/var/log/kuzco/"
	logFilePath  = filepath.Join(logDirectory, "log.txt")
)

type discordMessage struct {
	warning       string
	critical      string
	criticalCount int
}

type DiscordWebhookPayload struct {
	Content string `json:"content"`
}

func newDiscordMessage(warning string, critical string) *discordMessage {
	return &discordMessage{
		warning:       warning,
		critical:      critical,
		criticalCount: 0,
	}
}

func (d *discordMessage) Send() {
	discordWebhookURL := os.Getenv("DISCORD_WEBHOOK_URL")

	hostname, err := os.Hostname()
	if err != nil {
		fmt.Println("Error getting hostname:", err)
	}

	var message string
	if d.criticalCount >= 3 {
		message = fmt.Sprintf("hostname：%s - %s", hostname, d.critical)
		d.criticalCount = 0
	} else {
		d.criticalCount++
		message = fmt.Sprintf("hostname：%s - %s [%s]", hostname, d.warning, strconv.Itoa(d.criticalCount))
	}

	payload := DiscordWebhookPayload{
		Content: message,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		fmt.Println("Error encoding JSON payload:", err)
	}
	resp, err := http.Post(discordWebhookURL, "application/json", bytes.NewBuffer(payloadBytes))
	if err != nil {
		fmt.Println("Error sending Discord message:", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		fmt.Printf("Failed to send message, status code: %d\n", resp.StatusCode)
	}
}

func countFinish(filePath string) int {
	file, err := os.Open(filePath)
	if err != nil {
		fmt.Println("Error opening file:", err)
		return 0
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	finishCount := 0
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "finish") {
			finishCount++
		}
	}
	return finishCount
}

func clearLog(filePath string) {
	os.WriteFile(filePath, []byte{}, 0644)
}

func startKuzco() {
	fmt.Println("Starting Kuzco...")
	exec.Command("sh", "-c", fmt.Sprintf("kuzco worker start > %s 2>&1 &", logFilePath)).Run()
	time.Sleep(30 * time.Second)
}

func exitHandler() {
	fmt.Println("Exiting...")
	exec.Command("pkill", "-9", "kuzco").Run()
	clearLog(logFilePath)
	os.Exit(0)
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Println("Error loading .env file")
	}

	checkIntervalStr := os.Getenv("CHECK_INTERVAL")
	if checkIntervalStr == "" {
		log.Fatal("CHECK_INTERVAL environment variable is not set")
	}
	checkInterval, _ := strconv.Atoi(checkIntervalStr)

	restartWaitTimeStr := os.Getenv("RESTART_WAIT_TIME")
	if restartWaitTimeStr == "" {
		log.Fatal("RESTART_WAIT_TIME environment variable is not set")
	}
	restartWaitTime, err := time.ParseDuration(restartWaitTimeStr)
	if err != nil {
		log.Fatalf("Error converting RESTART_WAIT_TIME to duration: %v", err)
	}

	discordMessage := newDiscordMessage(
		"warning: kuzco node fails and performs a restart procedure",
		"critical: kuzco node has restarted more than 3 times, please check.",
	)

	if _, err := os.Stat(logDirectory); os.IsNotExist(err) {
		os.MkdirAll(logDirectory, 0755)
	}

	startKuzco()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		exitHandler()
	}()

	for {
		initialFinishCount := countFinish(logFilePath)
		log.Println("Initial number of 'finish'", initialFinishCount)

		time.Sleep(time.Duration(checkInterval) * time.Second)

		finalFinishCount := countFinish(logFilePath)
		log.Println("Currently number of 'finish'", finalFinishCount)

		if finalFinishCount > initialFinishCount {
			log.Println("kuzco is healty!")
		} else {
			log.Println("kuzco anomaly detected, attempting to reboot in progress...")
			exec.Command("pkill", "-9", "kuzco").Run()
			exec.Command("pkill", "-9", "ollama").Run()
			time.Sleep(restartWaitTime * time.Second)
			startKuzco()
			discordMessage.Send()
		}

		clearLog(logFilePath)
	}
}
