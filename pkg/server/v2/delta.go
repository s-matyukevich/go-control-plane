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

package server

import (
	"errors"
	"strconv"
	"sync/atomic"

	discovery "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	core "github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v2"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type deltaStream interface {
	grpc.ServerStream

	Send(*discovery.DeltaDiscoveryResponse) error
	Recv() (*discovery.DeltaDiscoveryRequest, error)
}

func createDeltaResponse(resp cache.DeltaResponse, typeURL string) (*discovery.DeltaDiscoveryResponse, error) {
	if resp == nil {
		return nil, errors.New("missing response")
	}

	marshalledResponse, err := resp.GetDeltaDiscoveryResponse()
	if err != nil {
		return nil, err
	}

	return marshalledResponse, nil
}

func (s *server) deltaHandler(stream deltaStream, typeURL string) error {
	// a channel for receiving incoming delta requests
	reqCh := make(chan *discovery.DeltaDiscoveryRequest)
	reqStop := int32(0)

	go func() {
		for {
			req, err := stream.Recv()
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

	err := s.processDelta(stream, reqCh, typeURL)

	// prevents writing to a closed channel if send failed on blocked recv
	// TODO(kuat) figure out how to unblock recv through gRPC API
	atomic.StoreInt32(&reqStop, 1)

	return err
}

func (s *server) processDelta(stream deltaStream, reqCh <-chan *discovery.DeltaDiscoveryRequest, defaultTypeURL string) error {
	// increment stream count
	streamID := atomic.AddInt64(&s.streamCount, 1)

	// unique nonce generator for req-resp pairs per xDS stream; the server
	// ignores stale nonces. nonce is only modified within send() function.
	var streamNonce int64

	// a collection of watches per request type
	var values watches
	defer func() {
		values.Cancel()
		if s.callbacks != nil {
			s.callbacks.OnStreamClosed(streamID)
		}
	}()

	// sends a response by serializing to protobuf Any
	send := func(resp cache.DeltaResponse, typeURL string) (string, error) {
		out, err := createDeltaResponse(resp, typeURL)
		if err != nil {
			return "", err
		}

		// increment nonce
		streamNonce = streamNonce + 1
		out.Nonce = strconv.FormatInt(streamNonce, 10)
		if s.callbacks != nil {
			s.callbacks.OnStreamDeltaResponse(streamID, resp.GetDeltaRequest(), out)
		}
		return out.Nonce, stream.Send(out)
	}

	if s.callbacks != nil {
		if err := s.callbacks.OnStreamOpen(stream.Context(), streamID, defaultTypeURL); err != nil {
			return err
		}
	}

	// node may only be set on the first discovery request
	var node = &core.Node{}

	for {
		select {
		case <-s.ctx.Done():
			return nil
			// config watcher can send the requested resources types in any order
		case resp, more := <-values.deltaEndpoints:
			if !more {
				return status.Errorf(codes.Unavailable, "endpoints watch failed")
			}
			nonce, err := send(resp, resource.EndpointType)
			if err != nil {
				return err
			}
			// set state version info
			s.deltaLock.Lock()
			s.deltaVersions[resp.GetDeltaRequest().GetTypeUrl()], err = resp.GetSystemVersion()
			s.deltaLock.Unlock()
			if err != nil {
				return err
			}
			values.deltaEndpointNonce = nonce

		case resp, more := <-values.deltaClusters:
			if !more {
				return status.Errorf(codes.Unavailable, "clusters watch failed")
			}
			nonce, err := send(resp, resource.ClusterType)
			if err != nil {
				return err
			}
			// set state version info
			s.deltaLock.Lock()
			s.deltaVersions[resp.GetDeltaRequest().GetTypeUrl()], err = resp.GetSystemVersion()
			s.deltaLock.Unlock()
			if err != nil {
				return err
			}
			values.deltaClusterNonce = nonce

		case resp, more := <-values.deltaRoutes:
			if !more {
				return status.Errorf(codes.Unavailable, "routes watch failed")
			}
			nonce, err := send(resp, resource.RouteType)
			if err != nil {
				return err
			}
			// set state version info
			s.deltaLock.Lock()
			s.deltaVersions[resp.GetDeltaRequest().GetTypeUrl()], err = resp.GetSystemVersion()
			s.deltaLock.Unlock()
			if err != nil {
				return err
			}
			values.deltaRouteNonce = nonce

		case resp, more := <-values.deltaListeners:
			if !more {
				return status.Errorf(codes.Unavailable, "listeners watch failed")
			}
			nonce, err := send(resp, resource.ListenerType)
			if err != nil {
				return err
			}
			// set state version info
			s.deltaLock.Lock()
			s.deltaVersions[resp.GetDeltaRequest().GetTypeUrl()], err = resp.GetSystemVersion()
			s.deltaLock.Unlock()
			if err != nil {
				return err
			}
			values.deltaListenerNonce = nonce

		case resp, more := <-values.deltaSecrets:
			if !more {
				return status.Errorf(codes.Unavailable, "secrets watch failed")
			}
			nonce, err := send(resp, resource.SecretType)
			if err != nil {
				return err
			}
			// set state version info
			s.deltaLock.Lock()
			s.deltaVersions[resp.GetDeltaRequest().GetTypeUrl()], err = resp.GetSystemVersion()
			s.deltaLock.Unlock()
			if err != nil {
				return err
			}
			values.deltaSecretNonce = nonce

		case resp, more := <-values.deltaRuntimes:
			if !more {
				return status.Errorf(codes.Unavailable, "runtimes watch failed")
			}
			nonce, err := send(resp, resource.RuntimeType)
			if err != nil {
				return err
			}
			// set state version info
			s.deltaLock.Lock()
			s.deltaVersions[resp.GetDeltaRequest().GetTypeUrl()], err = resp.GetSystemVersion()
			s.deltaLock.Unlock()
			if err != nil {
				return err
			}
			values.deltaRuntimeNonce = nonce

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
				s.log.Errorf("received error from envoy: %s", req.ErrorDetail.String())
			}

			// node field in discovery request is delta-compressed
			// nonces can be reused across streams; we verify nonce only if nonce is not initialized
			var nonce string
			if req.Node != nil {
				node = req.Node
				nonce = req.GetResponseNonce()
			} else {
				req.Node = node
				// If we have no nonce, i.e. this is the first request on a delta stream, set one
				nonce = strconv.FormatInt(streamNonce, 10)
			}

			// type URL is required for ADS but is implicit for xDS
			if defaultTypeURL == resource.AnyType {
				if req.TypeUrl == "" {
					return status.Errorf(codes.InvalidArgument, "type URL is required for ADS")
				}
			} else if req.TypeUrl == "" {
				req.TypeUrl = defaultTypeURL
			}

			if s.callbacks != nil {
				if err := s.callbacks.OnStreamDeltaRequest(streamID, req); err != nil {
					return err
				}
			}

			// cancel existing watches to (re-)request a newer version
			switch {
			case req.TypeUrl == resource.EndpointType && (values.deltaEndpointNonce == "" || values.deltaEndpointNonce == nonce):
				if values.deltaEndpointCancel != nil {
					values.deltaEndpointCancel()
				}
				s.deltaLock.RLock()
				values.deltaEndpoints, values.deltaEndpointCancel = s.cache.CreateDeltaWatch(*req, s.deltaVersions[req.GetTypeUrl()])
				s.deltaLock.RUnlock()
			case req.TypeUrl == resource.ClusterType && (values.deltaClusterNonce == "" || values.deltaClusterNonce == nonce):
				if values.deltaClusterCancel != nil {
					values.deltaClusterCancel()
				}
				s.deltaLock.RLock()
				values.deltaClusters, values.deltaClusterCancel = s.cache.CreateDeltaWatch(*req, s.deltaVersions[req.GetTypeUrl()])
				s.deltaLock.RUnlock()
			case req.TypeUrl == resource.RouteType && (values.deltaRouteNonce == "" || values.deltaRouteNonce == nonce):
				if values.deltaRouteCancel != nil {
					values.deltaRouteCancel()
				}
				s.deltaLock.RLock()
				values.deltaRoutes, values.deltaRouteCancel = s.cache.CreateDeltaWatch(*req, s.deltaVersions[req.GetTypeUrl()])
				s.deltaLock.RUnlock()
			case req.TypeUrl == resource.ListenerType && (values.deltaListenerNonce == "" || values.deltaListenerNonce == nonce):
				if values.deltaListenerCancel != nil {
					values.deltaListenerCancel()
				}
				s.deltaLock.RLock()
				values.deltaListeners, values.deltaListenerCancel = s.cache.CreateDeltaWatch(*req, s.deltaVersions[req.GetTypeUrl()])
				s.deltaLock.RUnlock()
			case req.TypeUrl == resource.SecretType && (values.deltaSecretNonce == "" || values.deltaSecretNonce == nonce):
				if values.deltaSecretCancel != nil {
					values.deltaSecretCancel()
				}
				s.deltaLock.RLock()
				values.deltaSecrets, values.deltaSecretCancel = s.cache.CreateDeltaWatch(*req, s.deltaVersions[req.GetTypeUrl()])
				s.deltaLock.RUnlock()
			case req.TypeUrl == resource.RuntimeType && (values.deltaRuntimeNonce == "" || values.deltaRuntimeNonce == nonce):
				if values.deltaRuntimeCancel != nil {
					values.deltaRuntimeCancel()
				}
				s.deltaLock.RLock()
				values.deltaRuntimes, values.deltaRuntimeCancel = s.cache.CreateDeltaWatch(*req, s.deltaVersions[req.GetTypeUrl()])
				s.deltaLock.RUnlock()
			}
		}
	}
}
