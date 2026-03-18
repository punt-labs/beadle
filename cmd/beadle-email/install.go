package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/paths"
)

func runInstall(g globalOpts, args []string) int {
	// 1. Create directory tree
	dataDir, err := paths.DataDir()
	if err != nil {
		g.errorf("%v", err)
		return 1
	}
	for _, sub := range []string{"", "secrets", "attachments"} {
		dir := filepath.Join(dataDir, sub)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			g.errorf("create %s: %v", dir, err)
			return 1
		}
	}
	fmt.Fprintf(os.Stderr, "created %s\n", dataDir)

	// 2. Create email.json if missing
	configPath := filepath.Join(dataDir, "email.json")
	if _, err := os.Stat(configPath); err == nil {
		fmt.Fprintf(os.Stderr, "config already exists at %s; skipping\n", configPath)
	} else {
		cfg := promptConfig()
		data, _ := json.MarshalIndent(cfg, "", "  ")
		data = append(data, '\n')
		tmp := configPath + ".tmp"
		if err := os.WriteFile(tmp, data, 0o640); err != nil {
			g.errorf("write config: %v", err)
			return 1
		}
		if err := os.Rename(tmp, configPath); err != nil {
			os.Remove(tmp)
			g.errorf("rename config: %v", err)
			return 1
		}
		fmt.Fprintf(os.Stderr, "wrote %s\n", configPath)
	}

	// 3. Register MCP server
	if _, err := exec.LookPath("claude"); err != nil {
		fmt.Fprintf(os.Stderr, "claude CLI not found — register manually: claude mcp add -s user beadle-email -- %s serve\n", selfPath())
	} else {
		cmd := exec.Command("claude", "mcp", "add", "-s", "user", "beadle-email", "--", selfPath(), "serve")
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "MCP registration failed: %v (register manually)\n", err)
		} else {
			fmt.Fprintln(os.Stderr, "MCP server registered")
		}
	}

	// 4. Run doctor
	fmt.Fprintln(os.Stderr)
	return runDoctor(g, configPath)
}

func runUninstall(g globalOpts, _ []string) int {
	removed := 0

	// 1. Remove MCP registration
	if _, err := exec.LookPath("claude"); err == nil {
		cmd := exec.Command("claude", "mcp", "remove", "beadle-email")
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "MCP removal failed (may not be registered): %v\n", err)
		} else {
			fmt.Fprintln(os.Stderr, "removed MCP registration")
			removed++
		}
	}

	// 2. Remove deployed commands
	home, _ := os.UserHomeDir()
	commandsDir := filepath.Join(home, ".claude", "commands")
	for _, name := range []string{"inbox.md", "mail.md", "send.md", "contacts.md"} {
		path := filepath.Join(commandsDir, name)
		if err := os.Remove(path); err == nil {
			fmt.Fprintf(os.Stderr, "removed %s\n", path)
			removed++
		}
	}

	// 3. Clean permissions from settings.json
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if cleanedPerms := cleanSettings(settingsPath); cleanedPerms > 0 {
		fmt.Fprintf(os.Stderr, "removed %d permission rule(s) from settings.json\n", cleanedPerms)
		removed++
	}

	if removed == 0 {
		fmt.Fprintln(os.Stderr, "nothing to remove")
	}
	return 0
}

func promptConfig() email.Config {
	scanner := bufio.NewScanner(os.Stdin)
	cfg := email.Config{}

	cfg.IMAPHost = prompt(scanner, "IMAP host", "127.0.0.1")
	cfg.IMAPPort = promptInt(scanner, "IMAP port", 1143)
	cfg.IMAPUser = prompt(scanner, "IMAP user (email)", "")
	cfg.SMTPPort = promptInt(scanner, "SMTP port", 1025)
	cfg.FromAddress = prompt(scanner, "From address", cfg.IMAPUser)
	cfg.GPGBinary = prompt(scanner, "GPG binary", "gpg")
	cfg.GPGSigner = prompt(scanner, "GPG signer email", cfg.FromAddress)

	return cfg
}

func prompt(scanner *bufio.Scanner, label, defaultVal string) string {
	if defaultVal != "" {
		fmt.Fprintf(os.Stderr, "%s [%s]: ", label, defaultVal)
	} else {
		fmt.Fprintf(os.Stderr, "%s: ", label)
	}
	if scanner.Scan() {
		val := strings.TrimSpace(scanner.Text())
		if val != "" {
			return val
		}
	}
	return defaultVal
}

func promptInt(scanner *bufio.Scanner, label string, defaultVal int) int {
	s := prompt(scanner, label, fmt.Sprintf("%d", defaultVal))
	n := defaultVal
	fmt.Sscanf(s, "%d", &n)
	return n
}

func selfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "beadle-email"
	}
	return exe
}

// cleanSettings removes beadle-related entries from settings.json.
// Returns the number of rules removed.
func cleanSettings(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return 0
	}

	removed := 0

	// Clean permissions.allow
	if perms, ok := settings["permissions"].(map[string]any); ok {
		if allow, ok := perms["allow"].([]any); ok {
			var cleaned []any
			for _, rule := range allow {
				s, ok := rule.(string)
				if ok && (strings.Contains(s, "beadle") || strings.Contains(s, "Skill(inbox)") || strings.Contains(s, "Skill(mail)") || strings.Contains(s, "Skill(send)") || strings.Contains(s, "Skill(contacts)")) {
					removed++
					continue
				}
				cleaned = append(cleaned, rule)
			}
			perms["allow"] = cleaned
		}
	}

	if removed == 0 {
		return 0
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return 0
	}
	out = append(out, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o640); err != nil {
		return 0
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return 0
	}
	return removed
}
