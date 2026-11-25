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
| server | an acceptor without ALPN protocols offers `h2` then `http/1.1` (as Go's server fills an empty NextProtos) | `Server::protocols` with unencrypted HTTP/2: a connection starting with the client preface is HTTP/2 (prior knowledge, as Go 1.24's UnencryptedHTTP2); no Upgrade: h2c (removed by RFC 9113) |
| client | offers `h2` then `http/1.1` by ALPN; speaks what the server chose | `ClientBuilder::http2_prior_knowledge()` (reqwest's name): HTTP/2 from the first byte |

`ClientBuilder::http1_only()` (reqwest) offers only http/1.1. On the
server, `Protocols` as Go's `http.Protocols`: `http1`, `http2`,
`unencrypted_http2` (by default the first two).

## Layout

```
lib/std/src/http/hpack/   std::http::hpack (public, as Go's x/net/http2/hpack)
  mod.navi      Encoder, Decoder, HeaderField: tables, integers, strings
  huffman.navi  the Huffman code (from Go's tables; decoded as the canonical code it is)
lib/std/src/http/h2/
  mod.navi      frames (read and checked, written), Core: a connection's state
                shared by its reader, writer and streams; stream bodies
  server.navi   a connection served
  client.navi   a client connection (ClientConn)
```

## Server connection

As Go's serverConn, but with a lock instead of the serve goroutine's
channels:

- A reader G reads frames and applies them to the connection's state
  (streams, HPACK decoder, windows), under the connection's Mutex (mu;
  a Condvar wakes the streams waiting on it).
- A writer G alone writes: frames are queued (an unbounded channel) and
  written in order, several to a write. So the reader never waits on the
  peer (a peer that does not read cannot stop this side reading).
- HEADERS (with CONTINUATION) ending a request's head starts a handler G
  for the stream with a Request whose Body reads the stream's pipe.
- DATA goes into the stream's pipe (bounded by the window the server
  gave); reading it sends WINDOW_UPDATE for the stream and the
  connection (batched: once half the window is used, as Go's).
- The handler's Response is written by its G: HEADERS (encoded and queued
  under hmu, since the encoder's table follows the order blocks are
  queued in; hmu is taken before mu where both are), then DATA in frames
  of at most the peer's MAX_FRAME_SIZE, each waiting for room in the
  stream's and connection's send windows. A request body the handler did
  not read to its end: RST_STREAM NO_ERROR after the response (as Go's).
- SETTINGS are acked; PING answered; GOAWAY from the peer stops new
  streams. (Server::close stops accepting; open connections go on until
  their idle timeout.)
- Limits (Go's defaults): MAX_CONCURRENT_STREAMS 250 (more: RST_STREAM
  REFUSED_STREAM), receive windows 1 MiB per stream and per connection,
  WINDOW_UPDATE once half is read; MAX_FRAME_SIZE 16 KiB read; header
  list at most the server's max_header_bytes (more: 431); HPACK table
  4096. CONNECT is answered 501.
- Connection errors send GOAWAY with the code and close; stream errors
  RST_STREAM. The idle timeout closes a connection with no streams.

## Client connection

As Go's ClientConn (the same Core: a reader G, a writer G):

- The client's HTTP/2 connections (shared by its clones) are kept per
  scheme+host+port; a request takes a stream on one that has room (the
  peer's MAX_CONCURRENT_STREAMS) and is not going away, else dials
  another. A stream's id and its HEADERS are queued in one step (under
  hmu), so ids go out in order.
- Receive windows: 4 MiB per stream, 1 GiB for the connection (Go's
  transport). Connections without streams close after 90s (Go's
  IdleConnTimeout).
- A request: HEADERS (pseudo-headers :method :scheme :authority :path,
  then the fields lowercased, Connection-specific ones dropped), the body
  as DATA within the send windows, END_STREAM.
- The reader G hands HEADERS to the waiting request (the Response), DATA
  to the response body's pipe; reading it sends WINDOW_UPDATE.
- A response body never read to its end keeps its stream (navi has no
  Drop). The request's timeout bounds every wait on the stream (window,
  head, body); when it passes the stream is reset (CANCEL).
- Refused streams (REFUSED_STREAM, or above a GOAWAY's last stream id)
  never reached the server: the request goes again on a new connection
  if its body can be sent again.

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
- tests/run/http2.navi: navi client and server over TLS and prior
  knowledge, HTTP/1.1 when either side wants it; 100 concurrent requests
  on one connection; 5 MB each way (flow control), streamed back without
  a length; a timeout resetting its stream; frames written by hand: PING,
  malformed requests (RST_STREAM PROTOCOL_ERROR), DATA after the request
  ended (STREAM_CLOSED), connection errors (GOAWAY with PROTOCOL_ERROR,
  COMPRESSION_ERROR, FLOW_CONTROL_ERROR, FRAME_SIZE_ERROR).
- By hand (an oracle, not in CI): curl --http2 and Go's client against the
  navi server (200 concurrent streams, 20 MB each way); the navi client
  against Go's server (TLS and prior knowledge). h2spec: not run yet.
