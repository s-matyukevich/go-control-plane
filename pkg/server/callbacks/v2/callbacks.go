package callbacks

import (
	"context"

	discovery "github.com/envoyproxy/go-control-plane/envoy/api/v2"
)

// Callbacks is a collection of callbacks inserted into the server operation.
// The callbacks are invoked synchronously
type Callbacks interface {
	// OnFetchRequest is called for each Fetch request. Returning an error will end processing of the
	// request and respond with an error.
	OnFetchRequest(context.Context, *discovery.DiscoveryRequest) error
	// OnFetchResponse is called immediately prior to sending a response.
	OnFetchResponse(*discovery.DiscoveryRequest, *discovery.DiscoveryResponse)

	// OnStreamOpen is called once an xDS stream is open with a stream ID and the type URL (or "" for ADS).
	// Returning an error will end processing and close the stream. OnStreamClosed will still be called.
	OnStreamOpen(context.Context, int64, string) error
	// OnStreamClosed is called immediately prior to closing an xDS stream with a stream ID.
	OnStreamClosed(int64)

	// SOTW xDS ==============

	// OnStreamRequest is called once a request is received on a stream.
	// Returning an error will end processing and close the stream. OnStreamClosed will still be called.
	OnStreamRequest(int64, *discovery.DiscoveryRequest) error
	// OnStreamResponse is called immediately prior to sending a response on a stream.
	OnStreamResponse(int64, *discovery.DiscoveryRequest, *discovery.DiscoveryResponse)

	// Delta xDS =============

	// OnStreamDeltaRequest is called once a request is received on a stream.
	// Returning an error will end processing and close the stream. OnStreamClosed will still be called.
	OnStreamDeltaRequest(int64, *discovery.DeltaDiscoveryRequest) error
	// OnStreamDelatResponse is called immediately prior to sending a response on a stream.
	OnStreamDeltaResponse(int64, *discovery.DeltaDiscoveryRequest, *discovery.DeltaDiscoveryResponse)
}
