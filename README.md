# ⚡ NX: Systemd User Process Manager

A fast, lightweight tool and interactive Terminal User Interface (TUI) to run, monitor, and manage local developer processes as user-level systemd daemons, with full terminal interactivity via `screen`.

---

## 💡 What is it for?
`nx` is designed for developers who want to run background processes (like dev servers, databases, queue workers, or scraping scripts) reliably on their local Linux machines without cluttering terminal tabs, yet retain the ability to instantly attach and interact with those processes at any time.

## ⚠️ The Problem
Running long-lived local processes during development usually comes with friction:
1. **Lost Terminals**: Keeping multiple terminal tabs or sessions open just to keep local servers running.
2. **Process Management**: Manually writing systemd `.service` files, placing them in `~/.config/systemd/user/`, and memorizing long commands (`systemctl --user daemon-reload`, `systemctl --user enable nx-app.service`, etc.).
3. **No Interactivity**: Background systemd processes standard input/output is redirected to journald, meaning you cannot interact with a debugger (like `pdb` or `node inspect`) or interactive CLI interfaces.
4. **Messy Cleanup**: Forgetting to stop processes, resulting in orphaned background tasks running on local ports.

## 🚀 The Solution (Why `nx`?)
`nx` provides a seamless bridge between **systemd's reliability** and **terminal multiplexer interactivity (`screen`)**:
* **Instant Daemonization**: Launch any command under systemd instantly (e.g., `nx npm run dev`). `nx` automatically generates, enables, and starts the systemd service for you.
* **Interactive Attachment**: By running your service inside systemd-managed `screen` sessions, you can attach to the live interactive session from the TUI or command line, run interactive debuggers or input commands, and safely detach (`Ctrl+A`, `D`) while the process keeps running in the background.
* **Streamlined TUI Dashboard**: Run `nx` with no arguments to open a beautiful, responsive Bubble Tea TUI manager to view, start, stop, restart, toggle autostart, attach, and cleanly delete/uninstall services.

---

## 🛠️ Installation

Simply run the compiled binary once from your project directory:
```bash
go build -o nx main.go && ./nx
```
`nx` will automatically detect local execution and copy/install itself globally to `~/.local/bin/nx`, verifying your system `$PATH` in the process.

To completely uninstall `nx` and clean up all configured services:
```bash
nx --uninstall
```

---

## 📖 How to Use It

### 1. Launch a Process in the Background
Run `nx` followed by the command you want to run inside your project directory:
```bash
nx npm run dev
# or
nx python main.py
```
`nx` will automatically:
1. Generate a systemd user service file named `nx-<folder-name>.service`.
2. Reload systemd, enable the service, and start it.
3. Attach your terminal directly to the interactive `screen` session. 

> [!TIP]
> **Detaching**: To leave the process running in the background and return to your shell, press **`Ctrl+A` followed by `d`**.
> **Terminating**: To stop the process completely, press **`Ctrl+C`** inside the attached screen session.

### 2. Manage Services via the TUI Dashboard
Run `nx` with no arguments to launch the management dashboard:
```bash
nx
```

Inside the dashboard:
* **`[↑/↓]` or `[k/j]`**: Move cursor between services.
* **`[a]`**: **Attach** to the selected running service's interactive screen session.
* **`[l]`**: View the systemd **Logs** (real-time `journalctl` stream).
* **`[s]`**: **Start** or **Stop** the selected service.
* **`[r]`**: **Restart** the selected service (if running).
* **`[e]`**: Toggle Autostart (**Enable/Disable** systemd load configuration).
* **`[d]`**: Step-by-step **Breakdown** (Tear-down/clean up the service: Stop -> Disable -> Delete unit file).
* **`[q]`**: **Quit** the dashboard.

---

## ⚙️ Architecture & Configurations
* **Service Files**: Generated in `~/.config/systemd/user/nx-<app-name>.service`.
* **State Management**: Handled natively by systemd (`systemctl --user`).
* **Environment Variables**: Automatically inherits your current `$PATH` and shell environment variables, ensuring scripts run exactly as they would in your current terminal session.
