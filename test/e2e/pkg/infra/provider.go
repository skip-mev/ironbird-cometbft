package infra

import (
	"context"
	"net"

	"github.com/cometbft/cometbft/libs/log"
	e2e "github.com/cometbft/cometbft/test/e2e/pkg"
)

// Provider defines an API for manipulating the infrastructure of a
// specific set of testnet infrastructure.
type Provider interface {
	InfraInit(ctx context.Context) error

	InfraCreate(ctx context.Context, ccOnly bool, confirm bool) error

	InfraCheck(ctx context.Context) error

	// Setup generates any necessary configuration for the infrastructure
	// provider during testnet setup.
	Setup(ctx context.Context, clean bool) error

	// Build creates a Docker image with the node binary.
	Build(ctx context.Context, fast bool) error

	// Starts the nodes passed as parameter. A nodes MUST NOT
	// be started twice before calling StopTestnet
	// If no nodes are passed, start the whole network
	StartNodes(ctx context.Context, nodes ...*e2e.Node) error

	// Stops the whole network
	StopTestnet(ctx context.Context, force bool) error

	// Cleanup destroys all remote infra resources and removes local data.
	Cleanup(ctx context.Context, exceptCC bool, confirm bool) error

	// Disconnects the node from the network
	Disconnect(ctx context.Context, node *e2e.Node) error

	// Reconnects the node to the network.
	// This should only be called after Disconnect
	Reconnect(ctx context.Context, name *e2e.Node) error

	Logs(ctx context.Context, tail bool, node *e2e.Node) error

	// Returns the provider's infrastructure data
	GetInfrastructureData() *e2e.InfrastructureData

	// Checks whether the node has been upgraded in this run
	CheckUpgraded(ctx context.Context, node *e2e.Node) (string, bool, error)

	// NodeIP returns the IP address of the node that is used to communicate
	// with other nodes in the network (the internal IP address in case of the
	// docker infra type and the external IP address otherwise).
	NodeIP(node *e2e.Node) net.IP
}

type ProviderData struct {
	Manifest           *e2e.Manifest
	ManifestPath       string
	DataDir            string
	Testnet            *e2e.Testnet
	InfrastructureData *e2e.InfrastructureData
	Terraform          *Terraform
	Logger             log.Logger
}

func NewProviderData(
	manifest *e2e.Manifest,
	manifestPath string,
	dir string,
	testnet *e2e.Testnet,
	infraData *e2e.InfrastructureData,
	logger log.Logger,
) ProviderData {
	// TODO: only create Terraform when in `infra` command. (Unless `infra` will
	// be used for when there's no command.)
	tf, err := NewTerraform()
	if err != nil {
		// It will panic later, if tf is ever used.
		logger.Warn("Could not create Terraform", "err", err)
	}
	return ProviderData{
		Manifest:           manifest,
		ManifestPath:       manifestPath,
		DataDir:            dir,
		Testnet:            testnet,
		InfrastructureData: infraData,
		Terraform:          tf,
		Logger:             logger.With("module", "infra"),
	}
}

// GetInfrastructureData returns the provider's infrastructure data.
func (pd ProviderData) GetInfrastructureData() *e2e.InfrastructureData {
	return pd.InfrastructureData
}
