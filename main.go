package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Contract: https://arbiscan.io/address/0x35Bcf3c30594191d53231E4FF333E8A770453e40
var bondingManager = common.HexToAddress("0x35Bcf3c30594191d53231E4FF333E8A770453e40")

// RoundsManager contract: https://arbiscan.io/address/0xdd6f56DcC28D3F5f27084381fE8Df634985cc39f
var roundsManager = common.HexToAddress("0xdd6f56DcC28D3F5f27084381fE8Df634985cc39f")

// maskRPCURL returns the scheme://host/path of the RPC URL, omitting userinfo, port, and query.
func maskRPCURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(invalid url)"
	}

	// Only show scheme://host/path (omit userinfo, port, query, fragment).
	masked := u.Scheme + "://" + u.Hostname()
	if u.Path != "" {
		masked += u.Path
	}
	return masked
}

// connectToRPC tries to connect to one of the provided RPC URLs and returns the first that works.
func connectToRPC(rpcs []string) (*ethclient.Client, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, url := range rpcs {
		c, err := ethclient.DialContext(ctx, url)
		if err == nil {
			_, err2 := c.BlockNumber(ctx)
			if err2 == nil {
				return c, url, nil
			}
			c.Close()
		}
	}
	return nil, "", fmt.Errorf("all RPCs failed")
}

// sendDiscordAlert sends a message to a Discord channel using a webhook, with color.
func sendDiscordAlert(webhookURL, message string, color int) error {
	payload := map[string]interface{}{
		"embeds": []map[string]interface{}{
			{
				"title":       "Livepeer Reward watcher Alert",
				"description": message,
				"color":       color,
			},
		},
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(webhookURL, "application/json", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// sendAlert sends alerts to messaging platforms based on configuration.
func sendAlert(botToken, chatID, discordWebhook, message string, color int) error {
	var failed []string
	if discordWebhook != "" {
		if err := sendDiscordAlert(discordWebhook, message, color); err != nil {
			log.Printf("Discord alert error: %v", err)
			failed = append(failed, "Discord")
		}
	}
	if botToken != "" && chatID != "" {
		if err := sendTelegramAlert(botToken, chatID, message); err != nil {
			log.Printf("Telegram alert error: %v", err)
			failed = append(failed, "Telegram")
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("alert failed for: %s", strings.Join(failed, ", "))
	}
	return nil
}

// sendTelegramAlert sends a message to a Telegram chat using a bot.
func sendTelegramAlert(botToken, chatID, message string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
	payload := map[string]string{"chat_id": chatID, "text": message, "parse_mode": "Markdown"}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func main() {
	// Parse command line flags.
	delayFlag := flag.Duration("delay", 2*time.Hour, "Time to wait after new round before warning (e.g. 2h, 30m)")
	checkIntervalFlag := flag.Duration("check-interval", 1*time.Hour, "How often to check and repeat warning if reward not called (e.g. 1h)")
	repeatFlag := flag.Bool("repeat", true, "Repeat warning every check-interval (true) or only send once per round (false)")
	disableSuccessAlertsFlag := flag.Bool("disable-success-alerts", false, "Disable alerts when rewards are successfully called (default: false)")
	disableRoundAlertsFlag := flag.Bool("disable-round-alerts", false, "Disable alerts when new rounds start (default: false)")
	enableRPCAlertsFlag := flag.Bool("enable-rpc-alerts", false, "Enable alerts for RPC disconnects/reconnects and subscription errors (default: false)")
	maxRetryTimeFlag := flag.Duration("max-retry-time", 30*time.Minute, "Max time to retry RPC connections before giving up (0 = retry forever)")
	flag.Parse()
	args := flag.Args()
	if len(args) < 1 {
		log.Fatalf("Usage: %s <orchestrator-address> [rpc1 rpc2 ...]", os.Args[0])
	}
	orch := common.HexToAddress(args[0])
	rpcs := []string{"https://arb1.arbitrum.io/rpc"}
	if len(args) > 1 {
		rpcs = args[1:]
	}

	// Load config values from environment.
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_CHAT_ID")
	discordWebhook := os.Getenv("DISCORD_WEBHOOK_URL")
	if discordWebhook == "" && (botToken == "" || chatID == "") {
		log.Fatal("Either DISCORD_WEBHOOK_URL or both TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID must be set in the environment")
	}

	// Main RPC failover loop.
	var currentRound uint64
	var roundStart time.Time
	rewardCalled := false
	sentWarning := false
	retryStartTime := time.Now()
	sentInitialMonitoringAlert := false
	for {
		// Stop if max retry time exceeded.
		if *maxRetryTimeFlag > 0 && time.Since(retryStartTime) > *maxRetryTimeFlag {
			fatalMsg := fmt.Sprintf("❌ Failed to connect to any RPC after %v, giving up and shutting down reward watcher!", *maxRetryTimeFlag)
			sendAlert(botToken, chatID, discordWebhook, fatalMsg, 0xFF0000)
			log.Fatalf("%s", fatalMsg)
		}

		// Try to connect to an RPC endpoint.
		client, usedRPC, err := connectToRPC(rpcs)
		if err != nil {
			log.Printf("RPC connection failed: %v", err)
			time.Sleep(30 * time.Second)
			continue
		}
		log.Printf("Connected to %s", maskRPCURL(usedRPC))

		// Load ABIs (downloaded at build time).
		bondingABIBytes, err := os.ReadFile("ABIs/BondingManager.json")
		if err != nil {
			log.Fatalf("failed to read BondingManager ABI file: %v (run 'make download-abis' to download ABIs)", err)
		}
		bondingABI, err := abi.JSON(strings.NewReader(string(bondingABIBytes)))
		if err != nil {
			log.Fatalf("failed to parse BondingManager ABI: %v", err)
		}
		roundsABIBytes, err := os.ReadFile("ABIs/RoundsManager.json")
		if err != nil {
			log.Fatalf("failed to read RoundsManager ABI file: %v (run 'make download-abis' to download ABIs)", err)
		}
		roundsABI, err := abi.JSON(strings.NewReader(string(roundsABIBytes)))
		if err != nil {
			log.Fatalf("failed to parse RoundsManager ABI: %v", err)
		}
		rewardEvent := bondingABI.Events["Reward"]
		newRoundEvent := roundsABI.Events["NewRound"]

		// Subscribe to events.
		rewardCh := make(chan types.Log)
		rewardSub, err := client.SubscribeFilterLogs(context.Background(), ethereum.FilterQuery{
			Addresses: []common.Address{bondingManager},
			Topics: [][]common.Hash{
				{rewardEvent.ID},
				{common.BytesToHash(orch.Bytes())},
			},
		}, rewardCh)
		if err != nil {
			log.Printf("Reward subscription failed: %v", err)
			client.Close()
			time.Sleep(5 * time.Second)
			continue
		}
		roundCh := make(chan types.Log)
		roundSub, err := client.SubscribeFilterLogs(context.Background(), ethereum.FilterQuery{
			Addresses: []common.Address{roundsManager},
			Topics: [][]common.Hash{
				{newRoundEvent.ID},
			},
		}, roundCh)
		if err != nil {
			log.Printf("NewRound subscription failed: %v", err)
			rewardSub.Unsubscribe()
			client.Close()
			time.Sleep(5 * time.Second)
			continue
		}

		// Round and Reward monitoring loop.
		log.Println("Monitoring started...")
		if !sentInitialMonitoringAlert {
			monitoringMsg := fmt.Sprintf(
				"🟢 Livepeer Reward watcher monitoring orchestrator [%s](https://explorer.livepeer.org/accounts/%s/delegating) on Arbitrum.",
				orch.Hex(), strings.ToLower(orch.Hex()))
			sendAlert(botToken, chatID, discordWebhook, monitoringMsg, 0x00FF00)
			sentInitialMonitoringAlert = true
		} else {
			recoveryMsg := fmt.Sprintf("✅ RPC connection restored to %s, resuming monitoring.", maskRPCURL(usedRPC))
			if *enableRPCAlertsFlag {
				sendAlert(botToken, chatID, discordWebhook, recoveryMsg, 0x00FF00)
			}
		}
		ticker := time.NewTicker(*checkIntervalFlag)
	monitorLoop:
		for {
			select {
			case err := <-rewardSub.Err():
				log.Printf("Reward subscription error: %v", err)
				if *enableRPCAlertsFlag {
					sendAlert(botToken, chatID, discordWebhook, fmt.Sprintf("⚠️ Reward subscription error: %v", err), 0xFF0000)
				}
				break monitorLoop
			case err := <-roundSub.Err():
				log.Printf("NewRound subscription error: %v", err)
				if *enableRPCAlertsFlag {
					sendAlert(botToken, chatID, discordWebhook, fmt.Sprintf("⚠️ NewRound subscription error: %v", err), 0xFF0000)
				}
				break monitorLoop
			case vLog := <-rewardCh:
				// Reward called for this round.
				rewardCalled = true
				address := strings.ToLower(orch.Hex())
				txHash := vLog.TxHash.Hex()
				alertMsg := fmt.Sprintf(
					"✅ Reward called for [%s](https://explorer.livepeer.org/accounts/%s/delegating) in round %d at block %d, [tx %s](https://arbiscan.io/tx/%s).",
					address, address, currentRound, vLog.BlockNumber, txHash, txHash)
				log.Println(alertMsg)
				if !*disableSuccessAlertsFlag {
					sendAlert(botToken, chatID, discordWebhook, alertMsg, 0x00FF00)
				}
			case vLog := <-roundCh:
				// New round started.
				var roundNum uint64
				if len(vLog.Topics) > 1 {
					roundNum = vLog.Topics[1].Big().Uint64()
				}
				currentRound = roundNum
				roundStart = time.Now()
				rewardCalled = false
				sentWarning = false
				log.Printf("New round %d started", currentRound)
				if !*disableRoundAlertsFlag {
					newRoundMsg := fmt.Sprintf("🔄 New round %d started.", currentRound)
					sendAlert(botToken, chatID, discordWebhook, newRoundMsg, 0x0099FF)
				}
			case <-ticker.C:
				if !rewardCalled && !roundStart.IsZero() {
					elapsed := time.Since(roundStart)
					if elapsed >= *delayFlag {
						if *repeatFlag || !sentWarning {
							address := strings.ToLower(orch.Hex())
							alertMsg := fmt.Sprintf(
								"❌ No reward called for [%s](https://explorer.livepeer.org/accounts/%s/delegating) in round %d after %s.",
								address, address, currentRound, delayFlag.String())
							log.Println(alertMsg)
							sendAlert(botToken, chatID, discordWebhook, alertMsg, 0xFF0000)
							sentWarning = true
						}
					}
				}
			}
		}

		// Cleanup state before reconnecting.
		ticker.Stop()
		rewardSub.Unsubscribe()
		roundSub.Unsubscribe()
		client.Close()
		time.Sleep(5 * time.Second) // Brief pause before trying to reconnect
		retryStartTime = time.Now() // Start retry timer
	}
}
