# 🐳 dogoctl - DigitalOcean Droplet & Kubernetes Manager

A beautiful, interactive terminal UI (TUI) for managing DigitalOcean droplets and Kubernetes clusters, inspired by k9s. Create, list, and manage your droplets and Kubernetes resources with a modern command-line interface.

![dogoctl](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)
![License](https://img.shields.io/badge/license-MIT-blue)
![Version](https://img.shields.io/badge/version-1.1.0-blue)

## ✨ Features

### Droplet Management
- 🖥️ **Interactive TUI**: Navigate and manage droplets with intuitive keyboard shortcuts
- 📋 **List Droplets**: View all your droplets with status indicators, region, size, and IP information
- ➕ **Create Droplets**: Interactive form with helpful hints to create new droplets
- 🗑️ **Delete Droplets**: Safe deletion with confirmation dialogs
- 🔌 **SSH Connection**: Quick SSH access to droplets directly from the TUI
- 🔄 **Real-time Refresh**: Reload droplet list with loading indicators
- 🎨 **Color-coded Status**: Visual indicators for droplet status (● active, ○ off, ◐ new)
- 📊 **Status Bar**: Shows droplet count and last refresh time
- 🔍 **Region Filtering**: Filter droplets by region
- 📱 **Responsive Layout**: Adapts to terminal window size dynamically
- ⚡ **Loading States**: Visual feedback during API operations

### Kubernetes Cluster Management
- ☸️ **Kubernetes Clusters**: View and manage DigitalOcean Kubernetes clusters
- 📦 **Resource Types**: Browse deployments, pods, services, daemonsets, statefulsets, PVCs, configmaps, secrets, nodes, and namespaces
- 🔄 **Command Mode**: Quick resource switching using `:` command (e.g., `:configmaps`)
- 🏷️ **Namespace Filtering**: Filter resources by namespace or view all namespaces
- 📊 **Cluster Info**: Display cluster details, version, region, and resource counts in the top panel
- 🔍 **Resource Details**: View detailed information about Kubernetes resources
- ⚡ **Real-time Updates**: Refresh cluster resources with loading indicators

## 📸 Screenshots

The interface features:
- Beautiful color-coded status indicators
- Clean, modern design with rounded borders
- Contextual help and hints
- Smooth animations and transitions
- k9s-inspired multi-panel layout
- Dynamic responsive design

## 🚀 Prerequisites

- Go 1.24 or later
- DigitalOcean API token
- Kubernetes clusters (for Kubernetes features)

## 📦 Installation

### From Source

1. Clone this repository:
```bash
git clone git@github.com:shakson1/dogoctl.git
cd dogoctl
```

2. Install dependencies:
```bash
go mod download
```

3. Build the application:
```bash
go build -o do-droplets
```

4. (Optional) Install to your PATH:
```bash
sudo mv do-droplets /usr/local/bin/
```

## ⚙️ Configuration

Set your DigitalOcean API token as an environment variable:

```bash
export DO_TOKEN=your_digitalocean_api_token_here
```

You can get your API token from the [DigitalOcean API Tokens page](https://cloud.digitalocean.com/account/api/tokens).

For persistent configuration, add it to your shell profile:
```bash
echo 'export DO_TOKEN=your_token_here' >> ~/.zshrc  # or ~/.bashrc
source ~/.zshrc
```

## 🎮 Usage

Run the application:

```bash
./do-droplets
```

Or if installed globally:
```bash
do-droplets
```

## ⌨️ Keyboard Shortcuts

### Main View (Droplets)
| Key | Action |
|-----|--------|
| `<1>` | Switch to Droplets view |
| `<2>` | Switch to Kubernetes Clusters view |
| `n` | Create a new droplet |
| `r` | Refresh the current view |
| `d` | Delete selected droplet (with confirmation) |
| `s` | SSH into selected droplet |
| `<enter>` | View droplet/cluster details |
| `<0-9>` | Filter by region (0 = all) |
| `↑/↓` | Navigate through items |
| `q` | Quit the application |

### Kubernetes Clusters View
| Key | Action |
|-----|--------|
| `<enter>` | Enter cluster and view resources |
| `<esc>` | Go back to clusters list |
| `r` | Refresh clusters list |
| `q` | Quit |

### Cluster Resources View
| Key | Action |
|-----|--------|
| `:` | Enter command mode (type resource name) |
| `d` | Cycle through resource types |
| `n` | Switch namespace (toggle all/specific) |
| `r` | Refresh resources |
| `<enter>` | View resource details |
| `<esc>` | Go back to clusters list |
| `q` | Quit |

### Command Mode (`:`)
Type resource name and press `<enter>` to switch:
- `deployments` - View deployments
- `pods` - View pods
- `services` - View services
- `daemonsets` - View daemonsets
- `statefulsets` - View statefulsets
- `pvc` - View PersistentVolumeClaims
- `configmaps` - View ConfigMaps
- `secrets` - View Secrets
- `nodes` - View nodes
- `namespaces` - View namespaces

Press `<esc>` to cancel command mode.

### Create Form
| Key | Action |
|-----|--------|
| `tab` | Move to next field |
| `shift+tab` | Move to previous field |
| `enter` | Create droplet (on last field) |
| `esc` | Cancel and return to list |

### Details View
| Key | Action |
|-----|--------|
| `esc` / `enter` / `backspace` | Return to list |
| `q` | Quit |

### Delete Confirmation
| Key | Action |
|-----|--------|
| `y` | Confirm deletion |
| `n` / `esc` | Cancel deletion |

## 📝 Creating a Droplet

1. Press `n` to open the create form
2. Fill in the required fields:
   - **Name**: Droplet name (e.g., `my-droplet`)
   - **Region**: Region slug (e.g., `nyc3`, `sfo3`, `ams3`)
   - **Size**: Size slug (e.g., `s-1vcpu-1gb`, `s-2vcpu-2gb`)
   - **Image**: Image slug (e.g., `ubuntu-22-04-x64`, `debian-12-x64`)
   - **Tags**: Comma-separated tags (e.g., `web,production`)
3. Use `Tab`/`Shift+Tab` to navigate between fields
4. Each field shows helpful hints when focused
5. Press `Enter` on the last field to create the droplet
6. Press `Esc` to cancel

## 🔌 SSH Connection

### Connecting to a Droplet

1. Select a droplet from the list using `↑/↓` arrow keys
2. Press `s` to SSH into the selected droplet
3. The TUI will exit and open an SSH session to the droplet's IP address
4. When you exit the SSH session (type `exit` or press `Ctrl+D`), you'll return to your shell

### Requirements

- The droplet must be in `active` status
- The droplet must have an IPv4 address assigned
- SSH must be configured on your system (the `ssh` command must be available)
- Your SSH keys must be set up for the droplet (either via DigitalOcean's key management or manually)

### Troubleshooting SSH

- **"droplet has no IP address"**: The droplet may still be provisioning. Wait a few moments and refresh (`r`)
- **"droplet is not active"**: The droplet must be running. Check the status in the TUI
- **"SSH connection failed"**: 
  - Verify your SSH keys are added to the droplet
  - Check that the droplet's firewall allows SSH (port 22)
  - Ensure your local SSH configuration is correct
  - Try connecting manually: `ssh root@<droplet-ip>`

## ☸️ Managing Kubernetes Clusters

### Viewing Cluster Resources

1. Press `<2>` to switch to Kubernetes Clusters view
2. Select a cluster and press `<enter>`
3. View resources by:
   - Pressing `:` and typing resource name (e.g., `:configmaps`)
   - Pressing `d` to cycle through resource types
4. Filter by namespace:
   - Press `n` to view namespaces
   - Select a namespace and press `<enter>`
   - Press `n` again to clear filter (show all)

### Available Resource Types

- **Deployments**: NAME, READY, UP-TO-DATE, AVAILABLE, AGE
- **Pods**: NAME, READY, STATUS, RESTARTS, AGE
- **Services**: NAME, TYPE, CLUSTER-IP, EXTERNAL-IP, AGE
- **DaemonSets**: NAME, READY, CURRENT, AGE
- **StatefulSets**: NAME, READY, AGE
- **PVC**: NAME, STATUS, CAPACITY, AGE
- **ConfigMaps**: NAME, DATA, AGE
- **Secrets**: NAME, TYPE, DATA, AGE
- **Nodes**: NAME, STATUS, ROLES, AGE, VERSION
- **Namespaces**: NAME, STATUS, AGE

## 🌍 Common DigitalOcean Values

### Regions
- `nyc1`, `nyc3` - New York
- `sfo3` - San Francisco
- `ams3` - Amsterdam
- `sgp1` - Singapore
- `lon1` - London
- `fra1` - Frankfurt
- `tor1` - Toronto
- `blr1` - Bangalore

### Sizes
- `s-1vcpu-1gb` - Basic (1 vCPU, 1GB RAM) - $6/mo
- `s-1vcpu-2gb` - Basic (1 vCPU, 2GB RAM) - $12/mo
- `s-2vcpu-2gb` - Basic (2 vCPU, 2GB RAM) - $18/mo
- `s-2vcpu-4gb` - Basic (2 vCPU, 4GB RAM) - $24/mo
- `s-4vcpu-8gb` - Basic (4 vCPU, 8GB RAM) - $48/mo

### Images
- `ubuntu-22-04-x64` - Ubuntu 22.04 LTS
- `ubuntu-24-04-x64` - Ubuntu 24.04 LTS
- `debian-12-x64` - Debian 12
- `fedora-40-x64` - Fedora 40
- `rockylinux-9-x64` - Rocky Linux 9

## 💡 Example

```bash
# Set your token
export DO_TOKEN=dop_v1_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx

# Run the application
./do-droplets

# Switch to clusters view
# Press <2>

# Enter a cluster
# Press <enter> on a cluster

# View configmaps using command mode
# Press : then type "configmaps" and press <enter>

# Filter by namespace
# Press <n> to view namespaces, select one, press <enter>
```

## 🐛 Troubleshooting

### "DO_TOKEN is not set" error
Make sure you've exported the `DO_TOKEN` environment variable:
```bash
export DO_TOKEN=your_token_here
```

### Connection errors
- Verify your API token is valid
- Check your internet connection
- Ensure you have sufficient API rate limits
- Verify your token has read/write permissions

### Kubernetes cluster access errors
- Ensure your DigitalOcean account has access to Kubernetes clusters
- Verify the cluster is running and accessible
- Check that the kubeconfig can be retrieved from DigitalOcean

### Build errors
Make sure all dependencies are installed:
```bash
go mod tidy
go mod download
```

### Terminal compatibility
The TUI works best with modern terminals that support:
- ANSI color codes
- UTF-8 encoding
- Minimum 80x24 character size

Recommended terminals:
- iTerm2 (macOS)
- Terminal.app (macOS)
- Windows Terminal (Windows)
- Alacritty
- Kitty

## 🛠️ Development

### Tech Stack
- [Bubbletea](https://github.com/charmbracelet/bubbletea) - TUI framework
- [Lipgloss](https://github.com/charmbracelet/lipgloss) - Styling
- [DigitalOcean Go SDK](https://github.com/digitalocean/godo) - DigitalOcean API client
- [Kubernetes client-go](https://github.com/kubernetes/client-go) - Kubernetes API client

### Building from Source
```bash
git clone git@github.com:shakson1/dogoctl.git
cd dogoctl
go build -o do-droplets
```

### Running Tests
```bash
go test ./...
```

## 📄 License

MIT License - see LICENSE file for details

## 🤝 Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## 🙏 Acknowledgments

- Inspired by [k9s](https://github.com/derailed/k9s)
- Built with [Bubbletea](https://github.com/charmbracelet/bubbletea)
- Powered by [DigitalOcean API](https://docs.digitalocean.com/reference/api/)

## 📞 Support

For issues, questions, or contributions, please open an issue on GitHub.

## 📋 Changelog

### v1.1.0 (2024)
- SSH connection support for droplets
- iTerm terminal optimization and compatibility
- Improved keybindings visibility (1, 2, n always shown)
- Enhanced top bar rendering for all terminal sizes
- Better terminal width detection and fallback handling
- Optimized panel layouts for consistent rendering across terminals

### v1.0.0 (2024)
- Initial release
- Droplet management (list, create, delete)
- Kubernetes cluster management
- Resource type browsing (deployments, pods, services, etc.)
- Command mode for quick resource switching
- Namespace filtering
- Responsive TUI design
- Region filtering for droplets

---

Made with ❤️ for the DigitalOcean community
