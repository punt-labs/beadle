package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

var healthPort int

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check WebSocket server health",
	Long:  "HTTP GET to http://localhost:<port>/health. Exit 0 if 200, exit 1 otherwise.",
	RunE: func(cmd *cobra.Command, args []string) error {
		url := fmt.Sprintf("http://localhost:%d/health", healthPort)
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(url)
		if err != nil {
			return fmt.Errorf("health check failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("health check returned %d", resp.StatusCode)
		}

		fmt.Println("ok")
		return nil
	},
}

func init() {
	healthCmd.Flags().IntVar(&healthPort, "port", 8420, "Server port to check")
}
