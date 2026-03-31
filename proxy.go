package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	loggedIPs              = map[string]bool{}
	forwardCounts          = map[string]int{}
	loggedForwarding       = map[string]bool{}
	verifiedIPs            = map[string]time.Time{}
	captchaReadErrorLogged = map[string]bool{}
	lastLogTime            = map[string]time.Time{}
	mu                     sync.Mutex
	logThreshold           = 100
	logResetTime           time.Time
)

func forward(src, dest net.Conn, direction string, wg *sync.WaitGroup) {
	defer wg.Done()
	clientIP := src.RemoteAddr().String()
	ip := clientIP[:strings.Index(clientIP, ":")]

	src.SetDeadline(time.Time{})
	dest.SetDeadline(time.Time{})

	bytesCopied, err := io.Copy(src, dest)
	if err != nil {
		mu.Lock()
		if !loggedIPs[ip] {
			loggedIPs[ip] = true
		}
		mu.Unlock()
		src.Close()
		dest.Close()
		return
	}
	mu.Lock()
	forwardCounts[ip]++
	if forwardCounts[ip] <= 2 {
		if !loggedForwarding[ip] {
			loggedForwarding[ip] = true
			log.Printf("Forwarded %d bytes (%s)\n", bytesCopied, direction)
		}
	}
	mu.Unlock()
	src.Close()
	dest.Close()
}

func shouldBypassCaptcha(ip string) bool {
	mu.Lock()
	defer mu.Unlock()
	if t, ok := verifiedIPs[ip]; ok {
		if time.Since(t) < 10*time.Minute {
			return true
		}
		delete(verifiedIPs, ip)
		log.Printf("Captcha session expired for %s", ip)
	}
	return false
}

func performCaptcha(conn net.Conn, reader *bufio.Reader, ip string, proto string) bool {
	a := rand.Intn(100)
	b := rand.Intn(100)
	answer := a + b
	var prompt string
	clear := "\033[2J\033[H"
	fmt.Fprintf(conn, "\033]0;Proxy Verification Gate\007")
	prompt = fmt.Sprintf("Captcha: %d \033[90m+ \033[0m%d \033[90m?\033[0m\r\n\033[90m~\033[0m> ", a, b)
	conn.Write([]byte(clear + prompt))
	if proto == "ssh" {
		conn.Write([]byte("(ssh banner shown; reconnect with telnet/raw to answer)\n"))
		return false
	}
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	line, err := reader.ReadString('\n')
	if err != nil {
		mu.Lock()
		if !captchaReadErrorLogged[ip] {
			captchaReadErrorLogged[ip] = true
		}
		mu.Unlock()
		return false
	}
	response := strings.TrimSpace(line)
	digits := ""
	for _, r := range response {
		if r >= '0' && r <= '9' {
			digits += string(r)
		}
	}
	if respInt, err := strconv.Atoi(digits); err == nil && respInt == answer {
		conn.Write([]byte(clear + "Captcha solved please reconnect to continue\033[92m!\033[0m "))
		time.Sleep(5 * time.Second)
		mu.Lock()
		verifiedIPs[ip] = time.Now()
		mu.Unlock()
	} else {
		conn.Write([]byte(clear + "Captcha failed\033[91m!\033[0m "))
		time.Sleep(5 * time.Second)
	}
	return false
}

func handleClient(client net.Conn, targetAddr string) {
	defer client.Close()
	clientIP := client.RemoteAddr().String()
	ip := clientIP[:strings.Index(clientIP, ":")]
	reader := bufio.NewReader(client)

	proto := "raw"
	if buf, err := reader.Peek(4); err == nil {
		if string(buf) == "SSH-" {
			proto = "ssh"
		} else if buf[0] == 0xFF {
			proto = "telnet"
		}
	}

	mu.Lock()
	if len(loggedIPs) > logThreshold && time.Since(logResetTime) > 5*time.Minute {
		loggedIPs = make(map[string]bool)
		logResetTime = time.Now()
	}
	if !loggedIPs[ip] {
		loggedIPs[ip] = true
		if len(loggedIPs) <= logThreshold {
			now := time.Now()
			if t, ok := lastLogTime[ip]; !ok || now.Sub(t) >= 10*time.Second {
				log.Printf("Client connected from %s\n", clientIP)
				lastLogTime[ip] = now
			}
		}
	}
	mu.Unlock()

	if !shouldBypassCaptcha(ip) {
		performCaptcha(client, reader, ip, proto)
		return
	}

	target, err := net.Dial("tcp", targetAddr)
	if err != nil {
		return
	}
	defer target.Close()
	mu.Lock()
	if !loggedIPs[targetAddr] {
		loggedIPs[targetAddr] = true
		log.Printf("Connected to backend server at %s\n", targetAddr)
	}
	mu.Unlock()
	if reader.Buffered() > 0 {
		if n, _ := io.CopyN(target, reader, int64(reader.Buffered())); n > 0 {
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go forward(client, target, "client->backend", &wg)
	go forward(target, client, "backend->client", &wg)
	wg.Wait()
}

func startProxy(listenAddr, targetAddr string) {
	mu.Lock()
	if !loggedIPs[listenAddr] {
		loggedIPs[listenAddr] = true
	}
	mu.Unlock()
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Printf("Failed to start tcp proxy on %s: %v\n", listenAddr, err)
		return
	}
	defer listener.Close()
	mu.Lock()
	if !loggedIPs[listenAddr] {
		loggedIPs[listenAddr] = true
		log.Printf("Proxy successfully listening on %s, forwarding to %s\n", listenAddr, targetAddr)
	}
	mu.Unlock()
	for {
		client, err := listener.Accept()
		if err != nil {
			continue
		}
		go handleClient(client, targetAddr)
	}
}

func main() {
	mu.Lock()
	verifiedIPs = make(map[string]time.Time)
	mu.Unlock()
	log.Printf("Captcha state reset")
	rand.Seed(time.Now().UnixNano())

	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		for range ticker.C {
			mu.Lock()
			if len(loggedIPs) > logThreshold {
				loggedIPs = make(map[string]bool)
				forwardCounts = make(map[string]int)
				loggedForwarding = make(map[string]bool)
				captchaReadErrorLogged = make(map[string]bool)
				logResetTime = time.Now()
				log.Printf("State maps cleared during periodic cleanup")
			}
			for ip, t := range verifiedIPs {
				if time.Since(t) > 10*time.Minute {
					delete(verifiedIPs, ip)
				}
			}
			mu.Unlock()

			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			if m.Alloc > 300*1024*1024 {
				log.Printf("High memory usage detected, clearing maps and forcing GC")
				mu.Lock()
				loggedIPs = make(map[string]bool)
				forwardCounts = make(map[string]int)
				loggedForwarding = make(map[string]bool)
				captchaReadErrorLogged = make(map[string]bool)
				mu.Unlock()
				runtime.GC()
			}
			runtime.GC()
		}
	}()
	if len(os.Args) < 3 || len(os.Args) > 4 {
		fmt.Println("Developed by: ----------> tcpfailed")
		fmt.Println("usage: ./proxy <cncserverip> <cncscreenport> <proxyport>")
		fmt.Println("example: ./proxy 127.0.0.1 1111 1738")
		fmt.Println("shortcut: ./proxy <cncserverip> <cncscreenport>  (defaults to proxyport=1738)")
		return
	}
	serverIP := os.Args[1]
	backendPort := os.Args[2]
	forwardPort := "1337"
	if len(os.Args) == 4 {
		forwardPort = os.Args[3]
	} else {
		log.Printf("No proxy port provided; using default port %s", forwardPort)
	}

	if _, err := strconv.Atoi(backendPort); err != nil || backendPort == "0" {
		log.Fatalf("Invalid backend port: %s", backendPort)
	}
	if _, err := strconv.Atoi(forwardPort); err != nil || forwardPort == "0" {
		log.Fatalf("Invalid proxy port: %s", forwardPort)
	}

	listenAddr := fmt.Sprintf("0.0.0.0:%s", forwardPort)
	targetAddr := fmt.Sprintf("%s:%s", serverIP, backendPort)
	log.Printf("Initializing tcp proxy from %s to %s", listenAddr, targetAddr)
	log.Printf("\033[90m-----------------------------\033[0m")
	startProxy(listenAddr, targetAddr)
}
