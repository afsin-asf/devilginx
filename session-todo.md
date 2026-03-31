# Session System Refactor - Implementation Todo

## Phase 1: Database Layer Refactoring
- [ ] Add `CreateCredentialCapture(ip, userAgent, phishlet, ...)` method to db_session.go
- [ ] Add `UpdateCredentialCapture(ip, userAgent, data)` method to db_session.go
- [ ] Add public API methods to database.go

## Phase 2: Core Proxy Request Flow Removal
- [ ] Remove session creation on lure URL request (http_proxy.go line ~500)
- [ ] Remove p.sessions[] storage logic
- [ ] Remove session cookie creation on lure URL hit

## Phase 3: Session-based Access Validation Removal
- [ ] Remove session cookie check logic (http_proxy.go lines 560-590)
- [ ] Remove `if ps.SessionId == "" && p.handleSession()` blocking logic
- [ ] Remove session existence checks that gate request handling

## Phase 4: Session-based Routing Removal
- [ ] Remove session lookup in js_inject paths
- [ ] Remove session lookup in dynamic redirect endpoints
- [ ] Disable session-specific redirect routing

## Phase 5: IP+UA-based Logging Index System
- [ ] Create `ipUAIndexMap` for tracking visitor indices by IP+UserAgent
- [ ] Implement first-request index assignment logic
- [ ] Implement index reuse for subsequent requests from same IP+UA

## Phase 6: Credential Capture Refactoring
- [ ] Replace session-based credential capture with IP+UA-based capture
- [ ] Update username/password capture to use IP+UA key
- [ ] Update body/HTTP/cookie token capture to use IP+UA key
- [ ] Remove session completion logic (http_proxy.go lines 1286-1320)

## Phase 7: ProxySession Struct Simplification
- [ ] Remove SessionId field from ProxySession struct
- [ ] Remove Created field from ProxySession struct
- [ ] Keep PhishDomain, PhishletName, Index fields
- [ ] Update all struct initializations

## Phase 8: Session Lookups Removal
- [ ] Replace all p.sessions[ps.SessionId] lookups with IP+UA lookups
- [ ] Remove remaining session existence checks

## Phase 9: Cloudflare Handling Verification
- [ ] Verify /cdn-cgi/challenge routing works without sessions
- [ ] Test dual-domain Cloudflare logic

## Phase 10: Admin Command Cleanup
- [ ] Remove session management commands from terminal.go
- [ ] Add new IP+UA credential listing commands (optional)

## Phase 11: Final Verification & Testing
- [ ] Landing page accessible without lure URL
- [ ] Credentials captured and stored per IP+UA combo
- [ ] Logging indices assigned and reused correctly per IP+UA
- [ ] Cloudflare challenges working end-to-end
- [ ] Browser cookies persist through login
- [ ] No session cookies in responses
- [ ] Compile and test

## Summary
- Total Phases: 11
- Status: Not started
