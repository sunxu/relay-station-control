package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

const maxConfigBytes = 64 << 10

type config struct {
	Listen        string `json:"listen"`
	ManagementKey string `json:"management_key"`
	Email         string `json:"email"`
	Token         string `json:"token"`
}

type counters struct {
	total        atomic.Uint32
	health       atomic.Uint32
	inventory    atomic.Uint32
	unauthorized atomic.Uint32
	rejected     atomic.Uint32
}

type fakeNode struct {
	managementKey []byte
	inventoryBody []byte
	counts        counters
}

func main() {
	configPath := flag.String("config", "", "path to protected fake-node configuration")
	flag.Parse()
	if flag.NArg() != 0 || *configPath == "" {
		fail("invalid_arguments")
	}
	configuration, err := loadConfig(*configPath)
	if err != nil {
		fail("invalid_config")
	}
	node, err := newFakeNode(configuration)
	if err != nil {
		fail("invalid_config")
	}
	listener, err := net.Listen("tcp", configuration.Listen)
	if err != nil {
		fail("listen_failed")
	}

	server := &http.Server{
		Handler:           node,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       2 * time.Second,
		WriteTimeout:      2 * time.Second,
		IdleTimeout:       2 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	fmt.Println("account_inventory_history_fake_node=ready")

	shutdown, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case <-shutdown.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err = server.Shutdown(ctx)
		cancel()
		if err != nil {
			fail("shutdown_failed")
		}
		if err = <-serveErrors; !errors.Is(err, http.ErrServerClosed) {
			fail("serve_failed")
		}
		fmt.Println(node.summary())
	case err = <-serveErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			fail("serve_failed")
		}
	}
}

func loadConfig(path string) (config, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return config{}, errors.New("invalid path")
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil || !parent.IsDir() || parent.Mode().Perm()&0o077 != 0 {
		return config{}, errors.New("unsafe parent")
	}
	information, err := os.Lstat(path)
	if err != nil || !information.Mode().IsRegular() || information.Mode().Perm()&0o077 != 0 ||
		information.Size() < 1 || information.Size() > maxConfigBytes {
		return config{}, errors.New("unsafe config")
	}
	file, err := os.Open(path)
	if err != nil {
		return config{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxConfigBytes+1))
	decoder.DisallowUnknownFields()
	var result config
	if err = decoder.Decode(&result); err != nil {
		return config{}, err
	}
	var extra any
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return config{}, errors.New("trailing config")
	}
	host, portText, err := net.SplitHostPort(result.Listen)
	if err != nil {
		return config{}, err
	}
	address := net.ParseIP(host)
	port, portErr := strconv.Atoi(portText)
	if address == nil || !address.IsLoopback() && !address.IsUnspecified() || portErr != nil || port < 1 || port > 65535 ||
		len(result.ManagementKey) < 1 || len(result.ManagementKey) > 64<<10 ||
		result.Email == "" || result.Email != strings.TrimSpace(result.Email) || len(result.Email) > 320 ||
		len(result.Token) < 1 || len(result.Token) > 64<<10 {
		return config{}, errors.New("invalid values")
	}
	return result, nil
}

func newFakeNode(configuration config) (*fakeNode, error) {
	body, err := json.Marshal(map[string]any{"files": []map[string]any{{
		"provider": "openai", "email": configuration.Email, "source": "memory",
		"status": "active", "disabled": false, "success": 9, "failed": 0,
		"token": configuration.Token,
	}}})
	if err != nil {
		return nil, err
	}
	return &fakeNode{managementKey: []byte(configuration.ManagementKey), inventoryBody: body}, nil
}

func (node *fakeNode) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	bump(&node.counts.total)
	if request.Method != http.MethodGet || request.URL.RawQuery != "" || request.ContentLength > 0 || len(request.TransferEncoding) != 0 {
		bump(&node.counts.rejected)
		http.Error(response, "request rejected", http.StatusMethodNotAllowed)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json")
	switch request.URL.Path {
	case "/healthz":
		if len(request.Header.Values("X-Management-Key")) != 0 {
			bump(&node.counts.rejected)
			http.Error(response, "request rejected", http.StatusBadRequest)
			return
		}
		bump(&node.counts.health)
		_, _ = io.WriteString(response, `{"status":"ok"}`)
	case "/v0/management/auth-files":
		values := request.Header.Values("X-Management-Key")
		if len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), node.managementKey) != 1 {
			bump(&node.counts.unauthorized)
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		bump(&node.counts.inventory)
		response.Header().Set("X-CPA-Version", "rollback-fake")
		response.Header().Set("X-CPA-Commit", "0000000")
		_, _ = response.Write(node.inventoryBody)
	default:
		bump(&node.counts.rejected)
		http.NotFound(response, request)
	}
}

func bump(counter *atomic.Uint32) {
	for value := counter.Load(); value != ^uint32(0); value = counter.Load() {
		if counter.CompareAndSwap(value, value+1) {
			return
		}
	}
}

func (node *fakeNode) summary() string {
	return fmt.Sprintf("account_inventory_history_fake_node=stopped total=%d health=%d inventory=%d unauthorized=%d rejected=%d",
		node.counts.total.Load(), node.counts.health.Load(), node.counts.inventory.Load(),
		node.counts.unauthorized.Load(), node.counts.rejected.Load())
}

func fail(reason string) {
	fmt.Fprintf(os.Stderr, "account_inventory_history_fake_node=failed reason=%s\n", reason)
	os.Exit(1)
}
