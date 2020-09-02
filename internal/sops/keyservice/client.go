// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package keyservice

import (
	"go.mozilla.org/sops/v3/keyservice"
	"golang.org/x/net/context"

	"google.golang.org/grpc"
)

// LocalClient is a key service client that performs all operations
// locally. The sole reason this exists is because the
// go.mozilla.org/sops/v3/keyservice.LocalClient does not implement
// the KeyServiceServer interface.
type LocalClient struct {
	Server keyservice.KeyServiceServer
}

// NewLocalClient creates a new local client that embeds the given
// KeyServiceServer.
func NewLocalClient(server keyservice.KeyServiceServer) LocalClient {
	return LocalClient{server}
}

// Decrypt processes a decrypt request locally.
func (c LocalClient) Decrypt(ctx context.Context,
	req *keyservice.DecryptRequest, opts ...grpc.CallOption) (*keyservice.DecryptResponse, error) {
	return c.Server.Decrypt(ctx, req)
}

// Encrypt processes an encrypt request locally.
func (c LocalClient) Encrypt(ctx context.Context,
	req *keyservice.EncryptRequest, opts ...grpc.CallOption) (*keyservice.EncryptResponse, error) {
	return c.Server.Encrypt(ctx, req)
}
