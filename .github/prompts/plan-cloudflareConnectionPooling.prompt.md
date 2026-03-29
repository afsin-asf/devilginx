# Cloudflare Challenge Connection Pooling Fix

## Problem Analysis
Multiple Cloudflare challenge validation POST requests to `/cdn-cgi/challenge-platform/...` fail with `tls: internal error` on attempts 1-2, but succeed on attempt 3+ after CONNECT handler reset.

### Root Cause
Current implementation creates a **new `http2.Transport` per request** via RoundTripper override:

```go
ctx.RoundTripper = goproxy.RoundTripperFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Response, error) {
    tr := createCustomUTLSTransport(ctx.ClientHelloSpec)  // ← NEW TRANSPORT EACH TIME
    return tr.RoundTrip(req)
})
```

**Why this fails:**
- [036] POST attempt 1 → New http2.Transport instance #1 → TLS error ❌
- [037] POST attempt 2 → New http2.Transport instance #2 → TLS error ❌
- [038] CONNECT handler → Reset connection
- [039] POST attempt 3 → Fresh state → 200 OK ✅

When ard arda requests reuse same connection on http2.Transport, multiplexing state gets corrupted → TLS internal error.

## Solution: Context-Aware Single Transport

### Changes Required

#### 1. Create Persistent Context-Aware Transport
Replace `createCustomUTLSTransport(spec)` with `createContextAwareTLSTransport()` that reads spec from request context:

```go
func createContextAwareTLSTransport() *http.Transport {
    return &http.Transport{
        Proxy: http.ProxyFromEnvironment,
        DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
            // Read ClientHelloSpec from request context
            spec, _ := ctx.Value("clientHelloSpec").(*utls.ClientHelloSpec)

            d := &net.Dialer{}
            rawConn, err := d.DialContext(ctx, network, addr)
            if err != nil {
                return nil, err
            }

            host, _, _ := net.SplitHostPort(addr)
            if host == "" {
                host = addr
            }

            tlsConfig := &utls.Config{
                ServerName:         host,
                InsecureSkipVerify: true,
                NextProtos:         []string{"h2", "http/1.1"},
            }

            var uConn *utls.UConn
            if spec != nil {
                // Use captured ClientHelloSpec
                uConn = utls.UClient(rawConn, tlsConfig, utls.HelloCustom)
                if err := uConn.ApplyPreset(spec); err != nil {
                    log.Debug("ApplyPreset failed: %v, falling back", err)
                    rawConn.Close()
                    rawConn2, _ := d.DialContext(ctx, network, addr)
                    uConn = utls.UClient(rawConn2, tlsConfig, utls.HelloChrome_Auto)
                }
            } else {
                // No spec available - use Chrome_Auto
                uConn = utls.UClient(rawConn, tlsConfig, utls.HelloChrome_Auto)
            }

            if err := uConn.HandshakeContext(ctx); err != nil {
                uConn.Close()
                return nil, err
            }

            return &http2CompatibleUTLSConn{uConn: uConn}, nil
        },
        // CRITICAL: Force new connections per request to allow fresh TLS handshakes
        DisableKeepAlives:   true,
        MaxConnsPerHost:     1,
        MaxIdleConnsPerHost: 0,
    }
}
```

#### 2. Set Transport During Initialization
In `NewHttpProxy()`, set the persistent transport **once**:

```go
// After: p.Proxy.Verbose = true
p.Proxy.Tr = createContextAwareTLSTransport()
```

#### 3. Simplify DoFunc - Remove RoundTripper Override
In the `DoFunc` lambda, ONLY set context - do NOT override RoundTripper:

```go
// OLD (DELETE THIS):
if ctx.ClientHelloSpec != nil {
    log.Debug("DoFunc: ClientHelloSpec captured - will use for upstream TLS")
    ctx.RoundTripper = goproxy.RoundTripperFunc(func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Response, error) {
        tr := createCustomUTLSTransport(ctx.ClientHelloSpec)
        return tr.RoundTrip(req)
    })
} else {
    log.Debug("DoFunc: ClientHelloSpec is NIL - no fingerprint available")
}

// NEW (REPLACE WITH):
if ctx.ClientHelloSpec != nil {
    log.Debug("DoFunc: ClientHelloSpec available, will use for upstream TLS connection")
    // Add ClientHelloSpec to request context for DialTLSContext to read
    req = req.WithContext(context.WithValue(req.Context(), "clientHelloSpec", ctx.ClientHelloSpec))
} else {
    log.Debug("DoFunc: No ClientHelloSpec captured yet, using default TLS")
}
// NOTE: Do NOT set ctx.RoundTripper - let p.Proxy.Tr (our persistent transport) handle it
```

## Flow After Fix

1. **First Cloudflare challenge POST**:
   - [036] CONNECT → ClientHello captured ✅
   - [036] POST request enters DoFunc
   - ClientHelloSpec set in request context ✅
   - `p.Proxy.Tr` (persistent) uses it via DialTLSContext ✅
   - **Result**: Upstream TLS uses Client's fingerprint ✅

2. **Second Cloudflare challenge POST (rapid succession)**:
   - [037] Same MITM connection
   - DoFunc runs again
   - ClientHelloSpec set in request context ✅
   - `p.Proxy.Tr` **reuses connection pool** but DisableKeepAlives=true forces new TCP
   - **Result**: New TLS connection with fresh handshake ✅
   - **NO TLS ERROR** ✅

3. **Cloudflare validation succeeds**:
   - Both POST requests complete successfully
   - `__cf_bm` cookie captured ✅
   - Session continues without infinite loop ✅

## Code Locations to Modify

1. **Line ~150-203**: Keep `createCustomUTLSTransport()` as-is (DEPRECATED but unused)
2. **Line ~204**: Add NEW `createContextAwareTLSTransport()` function
3. **Line ~240**: In `NewHttpProxy()`, add `p.Proxy.Tr = createContextAwareTLSTransport()`
4. **Line ~270-290**: In DoFunc, replace RoundTripper override logic with simple context.WithValue()

## Expected Outcomes

✅ **Cloudflare challenge POST requests**:
- No more `tls: internal error` on sequential requests
- Cookie validation succeeds on first attempt
- Challenge flow completes without infinite loops

✅ **Connection Pooling**:
- Single transport instance shared across all requests
- HTTP/2 multiplexing state stays valid
- New connections created only when needed (DisableKeepAlives + MaxConnsPerHost=1)

✅ **Fingerprinting**:
- Per-request ClientHelloSpec still applied
- Context propagation ensures spec reaches DialTLSContext
- Upstream connections use correct browser fingerprint

## Testing Checklist

- [ ] Build succeeds: `go build -o .\build\evilginx.exe -mod=mod`
- [ ] Hard refresh Firefox → No TLS errors in logs
- [ ] Cloudflare challenge → Single POST succeeds (no double POST)
- [ ] Check logs: No `tls: internal error` on challenge validation
- [ ] Verify `__cf_bm` cookie captured correctly
- [ ] Test with slow connection → Verify no connection pooling breaks
- [ ] Check regular requests (non-challenge) still use Firefox fingerprint
