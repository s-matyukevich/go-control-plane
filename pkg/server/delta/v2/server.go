// Copyright 2018 Envoyproxy Authors
//
//   Licensed under the Apache License, Version 2.0 (the "License");
//   you may not use this file except in compliance with the License.
//   You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
//   Unless required by applicable law or agreed to in writing, software
//   distributed under the License is distributed on an "AS IS" BASIS,
//   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//   See the License for the specific language governing permissions and
//   limitations under the License.

package delta

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"

	discovery "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	core "github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v2"
	"github.com/envoyproxy/go-control-plane/pkg/log"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v2"
	"github.com/envoyproxy/go-control-plane/pkg/server/stream/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server is defined to implement the specific stream handler type
type Server interface {
	DeltaStreamHandler(stream stream.DeltaStream, typeURL string) error
}

type Callbacks interface {
	// OnDeltaStreamOpen is called once an incremental xDS stream is open with a stream ID and the type URL (or "" for ADS).
	// Returning an error will end processing and close the stream. OnStreamClosed will still be called.
	OnDeltaStreamOpen(context.Context, int64, string) error
	// OnDeltaStreamClosed is called immediately prior to closing an xDS stream with a stream ID.
	OnDeltaStreamClosed(int64)
	// OnStreamDeltaRequest is called once a request is received on a stream.
	// Returning an error will end processing and close the stream. OnStreamClosed will still be called.
	OnStreamDeltaRequest(int64, *discovery.DeltaDiscoveryRequest) error
	// OnStreamDelatResponse is called immediately prior to sending a response on a stream.
	OnStreamDeltaResponse(int64, *discovery.DeltaDiscoveryRequest, *discovery.DeltaDiscoveryResponse)
}

// Options for modifying server behavior
type ServerOption func(*server)

// NewServer creates handlers from a config watcher and callbacks.
func NewServer(ctx context.Context, config cache.ConfigWatcher, callbacks Callbacks, log log.Logger, opts ...ServerOption) Server {
	out := &server{
		cache:         config,
		callbacks:     callbacks,
		ctx:           ctx,
		log:           log,
		xdsBufferSize: 1,
		muxBufferSize: 8,
	}
	for _, opt := range opts {
		opt(out)
	}
	return out
}

// WithADSBufferSize changes the size of the response channel for ADS handlers
// from the default 8. The size must be at least the number of different types
// on ADS to prevent dead locks between cache write and server read.
func WithADSBufferSize(size int) ServerOption {
	return func(s *server) {
		s.muxBufferSize = size
	}
}

// WithXDSBufferSize changes the size of the response channel for each xDS handler
// from the default 1. This buffer must be increased to support deferred cancellations
// for caches that can emit responses after cancel is called.
func WithXDSBufferSize(size int) ServerOption {
	return func(s *server) {
		s.xdsBufferSize = size
	}
}

type server struct {
	cache     cache.ConfigWatcher
	callbacks Callbacks

	// streamCount for counting bi-di streams
	streamCount int64
	ctx         context.Context

	// Channel buffer sizes
	xdsBufferSize int
	muxBufferSize int

	log log.Logger
}

// watches for all xDS resource types
type watches struct {
	mu *sync.RWMutex

	// Organize stream state by resource type
	deltaStreamStates map[string]stream.StreamState

	// Opaque resources share a muxed channel. Nonces and watch cancellations are indexed by type URL.
	deltaResponses     chan cache.DeltaResponse
	deltaCancellations map[string]func()
	deltaNonces        map[string]string
}

// Initialize all watches
func (values *watches) Init(bufferSize int) {
	// muxed channel needs a buffer to release go-routines populating it
	values.deltaResponses = make(chan cache.DeltaResponse, bufferSize)
	values.deltaNonces = make(map[string]string)
	values.deltaStreamStates = initStreamState()
	values.deltaCancellations = make(map[string]func())
	values.mu = &sync.RWMutex{}
}

var deltaErrorResponse = &cache.RawDeltaResponse{}

func initStreamState() map[string]stream.StreamState {
	m := make(map[string]stream.StreamState, 6)

	for i := 0; i < int(types.UnknownType); i++ {
		m[cache.GetResponseTypeURL(types.ResponseType(i))] = stream.StreamState{
			Nonce:            "",
			SystemVersion:    "",
			ResourceVersions: make(map[string]cache.DeltaVersionInfo, 0),
		}
	}

	return m
}

// Cancel all watches
func (values *watches) Cancel() {
	for _, cancel := range values.deltaCancellations {
		if cancel != nil {
			cancel()
		}
	}
}

func (s *server) processDelta(str stream.DeltaStream, reqCh <-chan *discovery.DeltaDiscoveryRequest, defaultTypeURL string) error {
	// increment stream count
	streamID := atomic.AddInt64(&s.streamCount, 1)

	// unique nonce generator for req-resp pairs per xDS stream; the server
	// ignores stale nonces. nonce is only modified within send() function.
	var streamNonce int64

	// a collection of stack alloceated watches per request type
	var values watches
	bufferSize := s.xdsBufferSize
	if defaultTypeURL == resource.AnyType {
		bufferSize = s.muxBufferSize
	}
	values.Init(bufferSize)

	defer func() {
		values.Cancel()
		if s.callbacks != nil {
			s.callbacks.OnDeltaStreamClosed(streamID)
		}
	}()

	// sends a response by serializing to protobuf Any
	send := func(resp cache.DeltaResponse) (string, error) {
		if resp == nil {
			return "", errors.New("missing response")
		}

		out, err := resp.GetDeltaDiscoveryResponse()
		if err != nil {
			return "", err
		}

		// increment nonce
		streamNonce = streamNonce + 1
		out.Nonce = strconv.FormatInt(streamNonce, 10)
		if s.callbacks != nil {
			s.callbacks.OnStreamDeltaResponse(streamID, resp.GetDeltaRequest(), out)
		}

		return out.Nonce, str.Send(out)
	}

	// updatest
	update := func(resp cache.DeltaResponse, nonce string) (stream.StreamState, error) {
		sv, err := resp.GetSystemVersion()
		if err != nil {
			return stream.StreamState{}, err
		}
		vm, err := resp.GetDeltaVersionMap()
		if err != nil {
			return stream.StreamState{}, err
		}

		return stream.StreamState{
			Nonce:            nonce,
			ResourceVersions: vm,
			SystemVersion:    sv,
		}, nil
	}

	process := func(resp cache.DeltaResponse) error {
		nonce, err := send(resp)
		if err != nil {
			return err
		}
		typeURL := resp.GetDeltaRequest().TypeUrl
		values.deltaNonces[typeURL] = nonce
		values.deltaCancellations[typeURL] = nil

		values.mu.Lock()
		values.deltaStreamStates[typeURL], err = update(resp, nonce)
		if err != nil {
			return err
		}
		values.mu.Unlock()

		return nil
	}

	processAll := func() error {
		for {
			select {
			case resp := <-values.deltaResponses:
				if err := process(resp); err != nil {
					return err
				}
			default:
				return nil
			}
		}
	}

	if s.callbacks != nil {
		if err := s.callbacks.OnDeltaStreamOpen(str.Context(), streamID, defaultTypeURL); err != nil {
			return err
		}
	}

	// node may only be set on the first discovery request
	var node = &core.Node{}

	for {
		select {
		case <-s.ctx.Done():
			if s.log != nil {
				s.log.Debugf("received signal to end! closing delta processor...")
			}

			return nil
		// config watcher can send the requested resources types in any order
		case resp := <-values.deltaResponses:
			if err := process(resp); err != nil {
				return err
			}
		case req, more := <-reqCh:
			// input stream ended or errored out
			if !more {
				return nil
			}
			if req == nil {
				return status.Errorf(codes.Unavailable, "empty request")
			}

			// Log out our error detail from envoy if we get one but don't do anything crazy here yet
			if req.ErrorDetail != nil {
				if s.log != nil {
					s.log.Errorf("received error from envoy: %s", req.ErrorDetail.String())
				}
			}

			// node field in discovery request is delta-compressed
			// nonces can be reused across streams; we verify nonce only if nonce is not initialized
			if req.Node != nil {
				node = req.Node
			} else {
				req.Node = node
			}

			var nonce = req.GetResponseNonce()

			// type URL is required for ADS but is implicit for xDS
			if defaultTypeURL == resource.AnyType {
				if req.TypeUrl == "" {
					return status.Errorf(codes.InvalidArgument, "type URL is required for ADS")
				}
			} else if req.TypeUrl == "" {
				req.TypeUrl = defaultTypeURL
			}

			// Handle our unsubscribe scenario (remove the tracked resources from the current state of the stream)
			if u := req.GetResourceNamesUnsubscribe(); len(u) > 0 {
				values.mu.Lock()
				s.unsubscribe(u, values.deltaStreamStates[req.GetTypeUrl()].GetVersionMap())
				values.mu.Unlock()
			}

			if s.callbacks != nil {
				if err := s.callbacks.OnStreamDeltaRequest(streamID, req); err != nil {
					return err
				}
			}

			// cancel existing watches to (re-)request a newer version
			typeURL := req.TypeUrl
			responseNonce, seen := values.deltaNonces[typeURL]
			if !seen || responseNonce == nonce {
				// We must signal goroutine termination to prevent a race between the cancel closing the watch
				// and the producer closing the watch.
				if cancel, seen := values.deltaCancellations[typeURL]; seen && cancel != nil {
					cancel()

					// Drain the current responses
					if err := processAll(); err != nil {
						return err
					}
				}

				values.mu.RLock()
				if values.deltaStreamStates != nil {
					values.deltaCancellations[typeURL] = s.cache.CreateDeltaWatch(req, values.deltaResponses, values.deltaStreamStates[typeURL])
				}
				values.mu.RUnlock()
			}
		}
	}
}

func (s *server) DeltaStreamHandler(str stream.DeltaStream, typeURL string) error {
	// a channel for receiving incoming delta requests
	reqCh := make(chan *discovery.DeltaDiscoveryRequest)
	reqStop := int32(0)

	go func() {
		for {
			req, err := str.Recv()
			if atomic.LoadInt32(&reqStop) != 0 {
				return
			}
			if err != nil {
				close(reqCh)
				return
			}
			reqCh <- req
		}
	}()

	err := s.processDelta(str, reqCh, typeURL)
	atomic.StoreInt32(&reqStop, 1)

	return err
}

func (s *server) unsubscribe(resources []string, sv map[string]cache.DeltaVersionInfo) {
	// here we need to search and remove from the current subscribed list in the snapshot
	for _, resource := range resources {
		if s.log != nil {
			s.log.Debugf("unsubscribing from resource: %s", resource)
		}
		delete(sv, resource)
	}
}
