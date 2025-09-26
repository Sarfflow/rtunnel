package main

import (
    "flag"
    "fmt"
    "os"
    "os/signal"
    "syscall"
    "rtunnel/tunnel"
    "strings"
)

func main() {
    flag.Usage = func() {
        fmt.Fprintf(os.Stderr, "Usage:\n")
        fmt.Fprintf(os.Stderr, "  Server mode:\n")
        fmt.Fprintf(os.Stderr, "    rtunnel <target_ip:port> <listen_port>\n")
        fmt.Fprintf(os.Stderr, "  Client mode:\n")
        fmt.Fprintf(os.Stderr, "    rtunnel <ws://url> <local_port>\n")
    }

    flag.Parse()
    args := flag.Args()

    if len(args) != 2 {
        flag.Usage()
        os.Exit(1)
    }

    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

    if strings.HasPrefix(args[0], "ws://") || strings.HasPrefix(args[0], "http://") {
        // Client mode
        client := tunnel.NewClient(args[0], args[1], sigChan)
        if err := client.Start(); err != nil {
            fmt.Fprintf(os.Stderr, "Client error: %v\n", err)
            os.Exit(1)
        }
    } else {
        // Server mode
        server := tunnel.NewServer(args[0], args[1], sigChan)
        if err := server.Start(); err != nil {
            fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
            os.Exit(1)
        }
    }
}
