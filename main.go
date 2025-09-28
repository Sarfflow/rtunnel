package main

import (
    "flag"
    "fmt"
    "net"
    "os"
    "strconv"
    "strings"
    "os/signal"
    "syscall"

    "rtunnel/logging"
	"rtunnel/tunnel"
)

// parseAddress 解析地址，支持 "ip:port" 或 "port" 格式
func parseAddress(addr string, defaultIP string) (string, error) {
	if _, err := strconv.Atoi(addr); err == nil {
		// 只有端口号，使用默认IP
		return net.JoinHostPort(defaultIP, addr), nil
	}

	// 检查是否是完整的 ip:port
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("invalid address format: %s", addr)
	}

	if host == "" {
		host = defaultIP
	}

	return net.JoinHostPort(host, port), nil
}

// isWebSocketURL 检查是否是WebSocket URL
func isWebSocketURL(url string) bool {
	return strings.HasPrefix(url, "ws://") || strings.HasPrefix(url, "wss://") ||
		strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
}

// convertToWebSocketURL 将HTTP/HTTPS URL转换为WS/WSS URL
func convertToWebSocketURL(url string) string {
	if strings.HasPrefix(url, "https://") {
		return "wss" + url[5:]
	}
	if strings.HasPrefix(url, "http://") {
		return "ws" + url[4:]
	}
	return url // 已经是ws/wss格式
}

func main() {
    // 定义可选的命令行参数
    var (
        certFile = flag.String("cert", "", "TLS certificate file (server mode)")
        keyFile  = flag.String("key", "", "TLS private key file (server mode)")
        secure   = flag.Bool("secure", false, "Enable secure mode (server: TLS, client: verify certificates)")
        debug    = flag.Bool("debug", false, "Enable debug logs and frequent error prints")
        showHelp = flag.Bool("help", false, "Show help message")
    )

    // 添加短选项
    flag.BoolVar(secure, "s", false, "Short for --secure")
    flag.BoolVar(debug, "d", false, "Short for --debug")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "RTunnel - WebSocket Tunnel Proxy\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  Server mode:  rtunnel <target_addr> <listen_port> [options]\n")
		fmt.Fprintf(os.Stderr, "  Client mode:  rtunnel <remote_url> <local_port> [options]\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  Server (HTTP):   rtunnel localhost:22 8080\n")
		fmt.Fprintf(os.Stderr, "  Server (HTTPS):  rtunnel localhost:22 8443 --cert server.crt --key server.key\n")
		fmt.Fprintf(os.Stderr, "  Client (WS):     rtunnel ws://localhost:8080 2222\n")
		fmt.Fprintf(os.Stderr, "  Client (WSS):    rtunnel wss://localhost:8443 2222 --secure\n")
		fmt.Fprintf(os.Stderr, "                   rtunnel https://localhost:8443 2222 -s\n\n")
		fmt.Fprintf(os.Stderr, "Address Format:\n")
		fmt.Fprintf(os.Stderr, "  Server target: <ip:port> or <port> (defaults to 127.0.0.1)\n")
		fmt.Fprintf(os.Stderr, "  Server listen: <port> (binds to 0.0.0.0)\n")
		fmt.Fprintf(os.Stderr, "  Client local:  <port> (binds to 127.0.0.1)\n")
	}

    // 允许选项出现在任意位置：将已知选项提前再解析
    flag.CommandLine.Parse(reorderArgs(os.Args[1:]))

    if *showHelp {
        flag.Usage()
        os.Exit(0)
    }

    // 设置全局调试开关，客户端与服务端均适用
    logging.SetDebug(*debug)

	args := flag.Args()
	if len(args) != 2 {
		fmt.Fprintf(os.Stderr, "Error: Invalid number of arguments\n\n")
		flag.Usage()
		os.Exit(1)
	}

    // 自动识别模式：如果第一个参数是URL格式，则为客户端模式
    if isWebSocketURL(args[0]) {
        // 客户端模式
        remoteURL := convertToWebSocketURL(args[0])
        localPort := args[1]

		// 解析本地绑定地址
		localAddr, err := parseAddress(localPort, "127.0.0.1")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing local port: %v\n", err)
			os.Exit(1)
		}

		// 提取端口号
		_, port, err := net.SplitHostPort(localAddr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing local port: %v\n", err)
			os.Exit(1)
		}

        client := tunnel.NewClient(remoteURL, port, !*secure)

        fmt.Printf("Starting client...\n")
        fmt.Printf("Remote URL: %s\n", remoteURL)
        fmt.Printf("Local bind: %s\n", localAddr)
        if *secure {
            fmt.Printf("TLS verification: enabled\n")
        } else {
            fmt.Printf("TLS verification: disabled (use --secure to enable)\n")
        }

        // 设置信号处理以优雅关闭客户端
        sigChan := make(chan os.Signal, 1)
        signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
        go func() {
            <-sigChan
            fmt.Println("\nShutting down client...")
            client.Stop()
        }()

        if err := client.Start(); err != nil {
            fmt.Fprintf(os.Stderr, "Client error: %v\n", err)
            os.Exit(1)
        }

	} else {
		// 服务端模式
		targetAddr := args[0]
		listenPort := args[1]

		// 解析地址
		targetFull, err := parseAddress(targetAddr, "127.0.0.1")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing target address: %v\n", err)
			os.Exit(1)
		}

		listenFull, err := parseAddress(listenPort, "0.0.0.0")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing listen port: %v\n", err)
			os.Exit(1)
		}

		// 提取监听端口号
		_, port, err := net.SplitHostPort(listenFull)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing listen port: %v\n", err)
			os.Exit(1)
		}

		server := tunnel.NewServer(targetFull, port)
		server.CertFile = *certFile
		server.KeyFile = *keyFile

		fmt.Printf("Starting server...\n")
		fmt.Printf("Target: %s\n", targetFull)
		fmt.Printf("Listen: %s\n", listenFull)

		if *certFile != "" && *keyFile != "" {
			fmt.Printf("TLS: enabled (%s, %s)\n", *certFile, *keyFile)
		} else {
			fmt.Printf("TLS: disabled (HTTP mode)\n")
		}

		if err := server.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
	}
}

// reorderArgs 将已知的选项（及其参数）前置，保持其它位置参数顺序不变
// 这样可以让 -d/--debug 等选项在任何位置都能被识别
func reorderArgs(argv []string) []string {
    var flags []string
    var positionals []string
    for i := 0; i < len(argv); i++ {
        tok := argv[i]
        if tok == "--" { // 遇到终止符，其后全部视为位置参数
            positionals = append(positionals, argv[i+1:]...)
            break
        }
        if strings.HasPrefix(tok, "-") {
            flags = append(flags, tok)
            // 处理需要值的长选项（支持 --key value 与 --key=value 两种形式）
            if needsValue(tok) && !strings.Contains(tok, "=") && i+1 < len(argv) {
                flags = append(flags, argv[i+1])
                i++
            }
        } else {
            positionals = append(positionals, tok)
        }
    }
    return append(flags, positionals...)
}

// needsValue 判断该长选项是否需要跟随一个值
func needsValue(tok string) bool {
    if strings.HasPrefix(tok, "--") {
        name := strings.TrimPrefix(tok, "--")
        if eq := strings.Index(name, "="); eq >= 0 {
            name = name[:eq]
        }
        switch name {
        case "cert", "key":
            return true
        }
    }
    return false
}