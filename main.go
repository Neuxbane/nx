package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	BinaryName = "nx"
)

var (
	HomeDir, _   = os.UserHomeDir()
	NxConfigDir  = filepath.Join(HomeDir, ".config", ".nx")
	SystemdDir   = filepath.Join(HomeDir, ".config", "systemd", "user")
	UserBinDir   = filepath.Join(HomeDir, ".local", "bin")
	SelfPath, _  = os.Executable()
	NxScreenDir  = filepath.Join(NxConfigDir, "sockets")
	ProjectsFile = filepath.Join(NxConfigDir, "projects.json")
)

func main() {
	ensureUserRuntime()

	if len(os.Args) > 1 && os.Args[1] == "--uninstall" {
		handleUninstall()
		return
	}

	ensureEnvironment()

	if len(os.Args) > 1 {
		handleCLIMode(os.Args[1:])
	} else {
		handleTUIMode()
	}
}

func ensureUserRuntime() {
	if os.Getenv("XDG_RUNTIME_DIR") != "" {
		return
	}

	uid := os.Getuid()
	runtimeDir := fmt.Sprintf("/run/user/%d", uid)

	if _, err := os.Stat(runtimeDir); os.IsNotExist(err) {
		if uid == 0 {
			// As root, we can start the user manager service for root
			exec.Command("systemctl", "start", "user@0.service").Run()
		}
	}

	if _, err := os.Stat(runtimeDir); err == nil {
		os.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	}
}

// -----------------------------------------------------------------------------
// Core Initialization & Installation Logic
// -----------------------------------------------------------------------------
func ensureEnvironment() {
	// 1. Ensure configuration and systemd directories exist
	if _, err := os.Stat(NxConfigDir); os.IsNotExist(err) {
		os.MkdirAll(NxConfigDir, 0755)
	}
	if _, err := os.Stat(SystemdDir); os.IsNotExist(err) {
		os.MkdirAll(SystemdDir, 0755)
	}
	if _, err := os.Stat(NxScreenDir); os.IsNotExist(err) {
		os.MkdirAll(NxScreenDir, 0700)
	}

	// 2. Strict Installation Check
	targetBinPath := filepath.Join(UserBinDir, BinaryName)
	
	if SelfPath != targetBinPath {
		fmt.Printf("📦 nx detected local execution (%s).\n", SelfPath)
		fmt.Printf("📥 Installing/Updating global binary to: %s...\n", targetBinPath)
		
		os.MkdirAll(UserBinDir, 0755)
		
		if err := copyFile(SelfPath, targetBinPath); err != nil {
			fmt.Printf("❌ Error installing binary: %v\n", err)
			os.Exit(1)
		}
		os.Chmod(targetBinPath, 0755)
		fmt.Println("✅ Global installation completed successfully!")

		// 3. AUTOMATIC $PATH GUARD CHECK
		checkSystemPath()
		
		// Exit early so the local runner ceases execution
		os.Exit(0)
	}
}

func checkSystemPath() {
	pathEnv := os.Getenv("PATH")
	paths := filepath.SplitList(pathEnv)
	
	isIncluded := false
	for _, p := range paths {
		if filepath.Clean(p) == filepath.Clean(UserBinDir) {
			isIncluded = true
			break
		}
	}

	if !isIncluded {
		fmt.Println("\n⚠️  WARNING: ~/.local/bin is NOT in your system $PATH variable!")
		fmt.Println("To be able to execute 'nx' from anywhere, add it by running:")
		
		shellEnv := os.Getenv("SHELL")
		if strings.Contains(shellEnv, "zsh") {
			fmt.Println("👉 echo 'export PATH=\"$HOME/.local/bin:$PATH\"' >> ~/.zshrc && source ~/.zshrc")
		} else {
			fmt.Println("👉 echo 'export PATH=\"$HOME/.local/bin:$PATH\"' >> ~/.bashrc && source ~/.bashrc")
		}
	} else {
		fmt.Println("🚀 Verified: ~/.local/bin is correctly bound into your system $PATH.")
		fmt.Println("⚡ You can now call 'nx' instantly from any directory.")
	}
}

func handleUninstall() {
	fmt.Println("🗑️ Uninstalling nx completely...")

	services := getNxServices()
	for _, s := range services {
		fmt.Printf("Stopping service %s...\n", s.Unit)
		exec.Command("systemctl", "--user", "stop", s.Unit).Run()
		exec.Command("systemctl", "--user", "disable", s.Unit).Run()
		os.Remove(filepath.Join(SystemdDir, s.Unit))
	}
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	exec.Command("systemctl", "--user", "reset-failed").Run()

	fmt.Printf("Removing config directory: %s\n", NxConfigDir)
	os.RemoveAll(NxConfigDir)

	targetBinPath := filepath.Join(UserBinDir, BinaryName)
	fmt.Printf("Removing binary: %s\n", targetBinPath)
	os.Remove(targetBinPath)

	fmt.Println("✨ nx has been completely uninstalled.")
}

// -----------------------------------------------------------------------------
// CLI Mode: Create New Process / Attach Log
// -----------------------------------------------------------------------------
func handleCLIMode(args []string) {
	cwd, _ := os.Getwd()
	folderName := filepath.Base(cwd)
	reg := regexp.MustCompile("[^a-z0-9-_]")
	safeName := reg.ReplaceAllString(strings.ToLower(folderName), "")
	if safeName == "" {
		safeName = "app"
	}

	store := loadProjects()
	serviceID, exists := store.Projects[cwd]
	if !exists {
		// Check if any other project has the same safeName
		var duplicates []string
		for path, id := range store.Projects {
			if strings.Contains(id, safeName) {
				duplicates = append(duplicates, path)
			}
		}

		if len(duplicates) > 0 {
			fmt.Printf("⚠️  Found %d other projects with the same folder name (%s)...\n", len(duplicates), safeName)
			fmt.Println("Choose one to run, or create a new unique one:")
			fmt.Println(" [n] Create New (Recommended)")
			for i, path := range duplicates {
				fmt.Printf(" [%d] %s\n", i+1, path)
			}

			var input string
			fmt.Print("👉 Selection: ")
			fmt.Scanln(&input)

			if input == "n" || input == "N" {
				uniqueName := generateUniqueID(safeName)
				serviceID = "nx-" + uniqueName
			} else {
				var idx int
				fmt.Sscanf(input, "%d", &idx)
				if idx > 0 && idx <= len(duplicates) {
					serviceID = store.Projects[duplicates[idx-1]]
				} else {
					fmt.Println("❌ Invalid selection. Creating new...")
					uniqueName := generateUniqueID(safeName)
					serviceID = "nx-" + uniqueName
				}
			}
		} else {
			serviceID = fmt.Sprintf("nx-%s", safeName)
		}

		// Save the mapping
		store.Projects[cwd] = serviceID
		saveProjects(store)
	}

	serviceFile := filepath.Join(SystemdDir, serviceID+".service")
	fullCmd := strings.Join(args, " ")
	livePath := os.Getenv("PATH")

	screenPath, err := exec.LookPath("screen")
	if err != nil {
		screenPath = "/usr/bin/screen"
	}

	unitContent := fmt.Sprintf(`[Unit]
Description=NX Managed Service - %[1]s
After=network.target

[Service]
Type=simple
WorkingDirectory=%[2]s
ExecStart=%[3]s -DmS %[4]s %[1]s
Restart=always
Environment=PATH=%[5]s SCREENDIR=%[6]s

[Install]
WantedBy=default.target
`, fullCmd, cwd, screenPath, serviceID, livePath, NxScreenDir)

	err = os.WriteFile(serviceFile, []byte(unitContent), 0644)
	if err != nil {
		fmt.Printf("Error creating service file: %v\n", err)
		return
	}

	fmt.Printf("🚀 Registering and launching daemon service: %s\n", serviceID)
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	exec.Command("systemctl", "--user", "enable", serviceID).Run()
	exec.Command("systemctl", "--user", "start", serviceID).Run()

	attachScreen(serviceID)
}

func attachScreen(serviceID string) {
	fmt.Printf("\n🔌 Attaching to screen session for %s...\n", serviceID)
	fmt.Println("👉 Press Ctrl+A followed by D to safely detach (Service will stay running in background)")

	var cmdErr error
	for i := 0; i < 5; i++ {
		time.Sleep(300 * time.Millisecond)
		cmd := exec.Command("screen", "-r", serviceID)
		cmd.Env = append(os.Environ(), fmt.Sprintf("SCREENDIR=%s", NxScreenDir))
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmdErr = cmd.Run()
		if cmdErr == nil {
			return
		}
	}

	fmt.Printf("⚠️  Could not attach to screen session: %v\n", cmdErr)
	streamLogs(serviceID)
}

func streamLogs(serviceID string) {
	fmt.Printf("\n--- Attaching to real-time logs for %s ---\n", serviceID)
	fmt.Println("👉 Press Ctrl+C to safely detach (Service will stay running in background)")
	
	cmd := exec.Command("journalctl", "--user-unit", serviceID, "-f", "-n", "50")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		fmt.Println("\n🔌 Detached cleanly from logs.")
		os.Exit(0)
	}()

	cmd.Run()
}

// -----------------------------------------------------------------------------
// TUI Dashboard Mode (Using Bubble Tea)
// -----------------------------------------------------------------------------
type ServiceInfo struct {
	Unit   string
	Active string 
	Sub    string 
	Loaded string 
}

type tuiModel struct {
	services    []ServiceInfo
	cursor      int
	confirmMode bool
	statusMsg   string
}

func handleTUIMode() {
	p := tea.NewProgram(tuiModel{services: getNxServices()})
	if _, err := p.Run(); err != nil {
		fmt.Printf("TUI Error: %v", err)
		os.Exit(1)
	}
}

func (m tuiModel) Init() tea.Cmd { return nil }

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.confirmMode {
			switch msg.String() {
			case "y", "Y":
				if len(m.services) > 0 {
					unit := m.services[m.cursor].Unit
					displayName := strings.TrimSuffix(unit, ".service")
					
					m.statusMsg = fmt.Sprintf("Deleting %s...", displayName)
					exec.Command("systemctl", "--user", "stop", unit).Run()
					exec.Command("systemctl", "--user", "disable", unit).Run()
					os.Remove(filepath.Join(SystemdDir, unit))
					
					exec.Command("systemctl", "--user", "daemon-reload").Run()
					exec.Command("systemctl", "--user", "reset-failed", unit).Run()
					exec.Command("systemctl", "--user", "reset-failed").Run() 
					
					m.services = getNxServices()
					m.confirmMode = false
					m.statusMsg = fmt.Sprintf("Deleted service %s successfully", displayName)
					if m.cursor >= len(m.services) && m.cursor > 0 {
						m.cursor--
					}
				}
			case "n", "N", "esc", "backspace", "d":
				m.confirmMode = false
				m.statusMsg = ""
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.statusMsg = ""
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			m.statusMsg = ""
			if m.cursor < len(m.services)-1 {
				m.cursor++
			}
		case "s":
			if len(m.services) > 0 {
				svc := m.services[m.cursor]
				displayName := strings.TrimSuffix(svc.Unit, ".service")
				if svc.Active == "active" {
					m.statusMsg = fmt.Sprintf("Stopping %s...", displayName)
					exec.Command("systemctl", "--user", "stop", svc.Unit).Run()
					m.statusMsg = fmt.Sprintf("Stopped %s successfully", displayName)
				} else {
					m.statusMsg = fmt.Sprintf("Starting %s...", displayName)
					exec.Command("systemctl", "--user", "reset-failed", svc.Unit).Run()
					exec.Command("systemctl", "--user", "start", svc.Unit).Run()
					m.statusMsg = fmt.Sprintf("Started %s successfully", displayName)
				}
				m.services = getNxServices()
			}
		case "r":
			if len(m.services) > 0 {
				svc := m.services[m.cursor]
				displayName := strings.TrimSuffix(svc.Unit, ".service")
				if svc.Active == "active" {
					m.statusMsg = fmt.Sprintf("Restarting %s...", displayName)
					exec.Command("systemctl", "--user", "restart", svc.Unit).Run()
					m.statusMsg = fmt.Sprintf("Restarted %s successfully", displayName)
					m.services = getNxServices()
				}
			}
		case "backspace", "d": 
			if len(m.services) == 0 {
				return m, nil
			}
			svc := m.services[m.cursor]
			displayName := strings.TrimSuffix(svc.Unit, ".service")

			isActive := svc.Active == "active"
			isEnabled := svc.Loaded == "enabled"

			if isActive {
				m.statusMsg = fmt.Sprintf("Stopping %s...", displayName)
				exec.Command("systemctl", "--user", "stop", svc.Unit).Run()
				m.statusMsg = fmt.Sprintf("Stopped %s (Breakdown)", displayName)
			} else if isEnabled {
				m.statusMsg = fmt.Sprintf("Disabling %s...", displayName)
				exec.Command("systemctl", "--user", "disable", svc.Unit).Run()
				m.statusMsg = fmt.Sprintf("Disabled %s (Breakdown)", displayName)
			} else {
				m.confirmMode = true
			}
			m.services = getNxServices()

		case "a":
			if len(m.services) > 0 {
				svc := m.services[m.cursor]
				if svc.Active == "active" {
					serviceID := strings.TrimSuffix(svc.Unit, ".service")
					cmd := exec.Command("screen", "-r", serviceID)
					cmd.Env = append(os.Environ(), fmt.Sprintf("SCREENDIR=%s", NxScreenDir))
					return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
						return nil
					})
				}
			}
		case "l":
			if len(m.services) > 0 {
				svc := m.services[m.cursor]
				return m, tea.ExecProcess(exec.Command("journalctl", "--user-unit", svc.Unit, "-f", "-n", "50"), func(err error) tea.Msg {
					return nil
				})
			}
		case "e":
			if len(m.services) > 0 {
				svc := m.services[m.cursor]
				displayName := strings.TrimSuffix(svc.Unit, ".service")
				if svc.Loaded == "enabled" {
					m.statusMsg = fmt.Sprintf("Disabling autostart for %s...", displayName)
					exec.Command("systemctl", "--user", "disable", svc.Unit).Run()
					m.statusMsg = fmt.Sprintf("Disabled autostart for %s", displayName)
				} else {
					m.statusMsg = fmt.Sprintf("Enabling autostart for %s...", displayName)
					exec.Command("systemctl", "--user", "enable", svc.Unit).Run()
					m.statusMsg = fmt.Sprintf("Enabled autostart for %s", displayName)
				}
				m.services = getNxServices()
			}
		}
	}
	return m, nil
}

func (m tuiModel) View() string {
	var s strings.Builder

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14")).Background(lipgloss.Color("8")).Padding(0, 1)
	s.WriteString(headerStyle.Render("NX SYSTEMD USER PROCESS MANAGER") + "\n\n")

	if len(m.services) == 0 {
		s.WriteString(" No nx processes active. Launch one inside a project directory using:\n 'nx <command>'\n")
	} else {
		s.WriteString(fmt.Sprintf("  %-25s %-12s %-12s %-10s\n", "SERVICE NAME", "ACTIVE", "SUB STATE", "AUTOSTART"))
		s.WriteString("  " + strings.Repeat("-", 64) + "\n")

		for i, svc := range m.services {
			displayName := strings.TrimSuffix(svc.Unit, ".service")
			
			activeStr := svc.Active
			if svc.Active == "active" {
				activeStr = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render(svc.Active)
			} else if svc.Active == "failed" || svc.Sub == "failed" {
				activeStr = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render("failed")
			}

			rowStr := fmt.Sprintf("  %-25s %-12s %-12s %-10s", displayName, activeStr, svc.Sub, svc.Loaded)

			if m.cursor == i && !m.confirmMode {
				selectedStyle := lipgloss.NewStyle().Background(lipgloss.Color("238"))
				s.WriteString(selectedStyle.Render(rowStr) + "\n")
			} else if m.cursor == i && m.confirmMode {
				alertStyle := lipgloss.NewStyle().Background(lipgloss.Color("88"))
				s.WriteString(alertStyle.Render(rowStr) + "\n")
			} else {
				s.WriteString(rowStr + "\n")
			}
		}
	}

	s.WriteString("\n" + strings.Repeat("─", 68) + "\n")
	
	if m.statusMsg != "" {
		statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Italic(true)
		s.WriteString(" ✨ " + statusStyle.Render(m.statusMsg) + "\n\n")
	}

	if m.confirmMode {
		promptStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
		s.WriteString(promptStyle.Render(" ⚠️  Are you sure you want to completely DELETE this unit file? (y/N): "))
	} else {
		footerStyle := lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("245"))
		var parts []string
		parts = append(parts, "[↑/↓] Move")
		
		if len(m.services) > 0 {
			svc := m.services[m.cursor]
			isActive := svc.Active == "active"
			isEnabled := svc.Loaded == "enabled"

			if isActive {
				parts = append(parts, "[a] Attach")
			}
			parts = append(parts, "[l] Logs")
			
			if isActive {
				parts = append(parts, "[s] Stop")
				parts = append(parts, "[r] Restart")
			} else {
				parts = append(parts, "[s] Start")
			}

			if isEnabled {
				parts = append(parts, "[e] Disable Autostart")
			} else {
				parts = append(parts, "[e] Enable Autostart")
			}

			if isActive {
				parts = append(parts, "[d] Stop (Breakdown)")
			} else if isEnabled {
				parts = append(parts, "[d] Disable (Breakdown)")
			} else {
				parts = append(parts, "[d] Delete (Breakdown)")
			}
		}
		parts = append(parts, "[q] Quit")
		s.WriteString(footerStyle.Render(" " + strings.Join(parts, " | ") + "\n"))
	}

	return s.String()
}

// Project mapping for path -> serviceID
type ProjectStore struct {
	Projects map[string]string `json:"projects"`
}

func loadProjects() ProjectStore {
	store := ProjectStore{Projects: make(map[string]string)}
	data, err := os.ReadFile(ProjectsFile)
	if err != nil {
		return store
	}
	json.Unmarshal(data, &store)
	return store
}

func saveProjects(store ProjectStore) {
	data, _ := json.MarshalIndent(store, "", "  ")
	os.WriteFile(ProjectsFile, data, 0644)
}

func generateUniqueID(folderName string) string {
	rand.Seed(time.Now().UnixNano())
	return fmt.Sprintf("%s-%d", folderName, rand.Intn(10000))
}

func getNxServices() []ServiceInfo {
	out, err := exec.Command("systemctl", "--user", "list-units", "nx-*", "--all", "--no-legend").Output()
	if err != nil {
		return []ServiceInfo{}
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var services []ServiceInfo
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		
		if fields[0] == "●" {
			fields = fields[1:]
		}

		if len(fields) >= 4 {
			unitName := fields[0]
			
			loadedStatus := "disabled"
			isEnabledOut, err := exec.Command("systemctl", "--user", "is-enabled", unitName).Output()
			if err == nil && strings.TrimSpace(string(isEnabledOut)) == "enabled" {
				loadedStatus = "enabled"
			}

			services = append(services, ServiceInfo{
				Unit:   unitName,
				Active: fields[2],
				Sub:    fields[3],
				Loaded: loadedStatus,
			})
		}
	}
	return services
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}