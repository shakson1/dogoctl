# 🐳 dogoctl - DigitalOcean Droplet Manager

A beautiful, interactive terminal UI (TUI) for managing DigitalOcean droplets, inspired by k9s. Create, list, and manage your droplets with a modern command-line interface.

![dogoctl](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat&logo=go)
![License](https://img.shields.io/badge/license-MIT-blue)

## ✨ Features

- 🖥️ **Interactive TUI**: Navigate and manage droplets with intuitive keyboard shortcuts
- 📋 **List Droplets**: View all your droplets with status indicators, region, size, and IP information
- ➕ **Create Droplets**: Interactive form with helpful hints to create new droplets
- 🗑️ **Delete Droplets**: Safe deletion with confirmation dialogs
- 🔄 **Real-time Refresh**: Reload droplet list with loading indicators
- 🎨 **Color-coded Status**: Visual indicators for droplet status (● active, ○ off, ◐ new)
- 📊 **Status Bar**: Shows droplet count and last refresh time
- 🔍 **Search/Filter**: Quickly find droplets by name
- 📱 **Responsive Layout**: Adapts to terminal window size
- ⚡ **Loading States**: Visual feedback during API operations

## 📸 Screenshots

The interface features:
- Beautiful color-coded status indicators
- Clean, modern design with rounded borders
- Contextual help and hints
- Smooth animations and transitions

## 🚀 Prerequisites

- Go 1.24 or later
- DigitalOcean API token

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

### Main View
| Key | Action |
|-----|--------|
| `n` | Create a new droplet |
| `r` | Refresh the droplet list |
| `d` | Delete selected droplet (with confirmation) |
| `enter` | View droplet details |
| `/` | Filter/search droplets |
| `↑/↓` | Navigate through droplets |
| `q` | Quit the application |

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

---

Made with ❤️ for the DigitalOcean community
