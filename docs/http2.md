# HTTP/2 in std::http

As Go's net/http: HTTP/2 is not a package of its own but a protocol the
server and the client speak when both can. The user's API stays the one of
std::http (Request, Response, handlers `fn(&mut Request) -> Response`,
Client); what changes is `Version::HTTP_2` on messages and a few switches.

RFC 9113 (HTTP/2), RFC 7541 (HPACK). Priorities (deprecated by RFC 9113)
are read and ignored; server push is not sent (the client says
SETTINGS_ENABLE_PUSH = 0, as Go's).

## When it is spoken

| | over TLS | cleartext |
|---|---|---|
| server | offers `h2` then `http/1.1` by ALPN (Go: a server with TLS) | `Server::protocols` with unencrypted HTTP/2: a connection starting with the client preface is HTTP/2 (prior knowledge, as Go 1.24's UnencryptedHTTP2); no Upgrade: h2c (removed by RFC 9113) |
| client | offers `h2` then `http/1.1` by ALPN; speaks what the server chose | `ClientBuilder::http2_prior_knowledge()` (reqwest's name): HTTP/2 from the first byte |

`ClientBuilder::http1_only()` (reqwest) offers only http/1.1. On the
server, `Protocols` as Go's `http.Protocols`: `http1`, `http2`,
`unencrypted_http2` (by default the first two).

## Layout

```
lib/std/src/http/h2/
  mod.navi      settings, error codes, the shared stream body pipe
  hpack.navi    encoder and decoder: static table, dynamic table, integers, strings
  huffman.navi  the HPACK Huffman code (encode; decode by a 4-bit state table as Go's)
  frame.navi    Framer: reads and writes the ten frame types, checks lengths and flags
  server.navi   a connection served
  client.navi   a client connection (ClientConn) and the pool's use of it
```

## Server connection

As Go's serverConn, but with a lock instead of the serve goroutine's
channels where that is simpler:

- A reader G reads frames and applies them to the connection's state
  (streams, HPACK decoder, windows), under the connection's Mutex.
- HEADERS (with CONTINUATION) ending a request's head starts a handler G
  for the stream with a Request whose Body reads the stream's pipe.
- DATA goes into the stream's pipe (bounded by the window the server
  gave); reading it sends WINDOW_UPDATE for the stream and the
  connection (batched: once half the window is used, as Go's).
- The handler's Response is written by its G: HEADERS (HPACK under the
  write lock, since the encoder's table is ordered by what is written),
  then DATA in frames of at most the peer's MAX_FRAME_SIZE, each waiting
  (Condvar) for room in the stream's and connection's send windows.
- Writes to the socket are serialized by a write Mutex; frames are written
  whole.
- SETTINGS are acked; PING answered; GOAWAY from the peer stops new
  streams; Server::close sends GOAWAY and lets running streams finish.
- Limits (Go's defaults): MAX_CONCURRENT_STREAMS 250, initial window 1 MiB
  per stream and per connection for uploads, MAX_FRAME_SIZE 16 KiB read,
  header list at most the server's max_header_bytes, HPACK table 4096.
- Connection errors send GOAWAY with the code and close; stream errors
  RST_STREAM. The idle timeout closes a connection with no streams.

## Client connection

As Go's ClientConn (a Mutex and a Condvar, a reader G):

- The pool keeps, per scheme+host+port, at most one HTTP/2 connection that
  takes new streams; a request takes a stream on it if it has room
  (peer's MAX_CONCURRENT_STREAMS) and is not going away, else waits or
  dials another.
- A request: HEADERS (pseudo-headers :method :scheme :authority :path,
  then the fields lowercased, Connection-specific ones dropped), the body
  as DATA within the send windows, END_STREAM.
- The reader G hands HEADERS to the waiting request (the Response), DATA
  to the response body's pipe; reading it sends WINDOW_UPDATE.
- A response body not read to its end and dropped: navi has no Drop, so
  as Go's: reading to EOF or `Response::close`-like reading is what frees
  the stream; a stream whose body is never read keeps its window. (A
  RST_STREAM CANCEL when the client's timeout ends the request.)
- GOAWAY: streams above its last stream id are retried on a new connection
  (idempotent requests, as Go's).

## Messages

- `Version::HTTP_2` (http crate's name); Debug prints "HTTP/2.0".
- Request/Response headers as in HTTP/1.1 (HeaderMap, lowercase names);
  `host` comes from :authority on the server.
- Content-Length is checked against the DATA received (a mismatch is a
  stream error, as RFC 9113 8.1.1 says).
- Trailers: not in this version (read and dropped on receipt).

## Tests

- tests/run/hpack.navi: RFC 7541 Appendix C (C.2 to C.6, with and without
  Huffman), integer edge cases, table eviction and size updates, bad input.
- tests/run/http2.navi: frames round trip and limits; navi client and
  server over TLS and prior knowledge; 100 concurrent requests on one
  connection; 10 MB each way (flow control); GOAWAY and RST_STREAM;
  a response body read slowly.
- By hand (an oracle, not in CI): curl --http2 and Go's client against the
  navi server; the navi client against Go's server.
