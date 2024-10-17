package digitalocean

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sync/errgroup"

	e2e "github.com/cometbft/cometbft/test/e2e/pkg"
	"github.com/cometbft/cometbft/test/e2e/pkg/exec"
	"github.com/cometbft/cometbft/test/e2e/pkg/infra"
)

const defaultRegion = "ams3"

const (
	sshOpts  = "-o LogLevel=ERROR -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o GlobalKnownHostsFile=/dev/null"
	psshOpts = "-O LogLevel=ERROR -O StrictHostKeyChecking=no -O UserKnownHostsFile=/dev/null -O GlobalKnownHostsFile=/dev/null"
)

var _ infra.Provider = (*Provider)(nil)

// Provider implements a DigitalOcean-backed infrastructure provider.
type Provider struct {
	infra.ProviderData
}

func (p *Provider) InfraInit(ctx context.Context) error {
	if err := p.Terraform.Init(ctx); err != nil {
		return err
	}
	return p.Terraform.Validate(ctx)
}

// Initial infrastructure data is obtained from the manifest. Without infra-data
// there's no testnet. Then the only place from where we can extract data to
// create infrastructure is the manifest.
func (p *Provider) InfraCreate(ctx context.Context, ccOnly bool, confirm bool) error {
	// Build variable assignments for applying Terraform.
	vars := []string{
		"testnet_dir=" + p.DataDir,
		"manifest_path=" + p.ManifestPath,
	}
	if !ccOnly {
		nodeNames := strings.Join(p.Manifest.SortNodeNames(), `","`)
		vars = append(vars, fmt.Sprintf("node_names=[\"%s\"]", nodeNames))
	}

	// Assign the same data-center region to all nodes.
	// TODO: validate Manifest.Region.
	region := p.Manifest.Region
	if region == "" {
		region = defaultRegion
	}
	vars = append(vars, "region="+region)

	p.Logger.Info("Terraform apply", "vars", vars)
	return p.Terraform.Apply(ctx, vars, confirm)
}

// InfraCheck provides user feedback if the CC server finished building.
func (p *Provider) InfraCheck(ctx context.Context) error {
	if err := p.Terraform.SetCCIP(ctx); err != nil {
		return err
	}
	if err := ssh(ctx, p.Terraform.CCIP, "cat /etc/cc"); err != nil {
		return err
	}
	p.logMonitorInfo(ctx)
	return nil
}

// Build will first call Create if CC server does not exist.
func (p *Provider) Build(ctx context.Context, _ bool) error {
	p.Logger.Info("Build binary locally and deploy it to CC server")

	// Call Create if CC server does not exist.
	// TODO: unreachable: Build won't be called because infra-data.json does not exists yet.
	err := p.Terraform.SetCCIP(ctx)
	if errors.Is(err, infra.ErrTerraformStateNotFound) {
		if err := p.InfraCreate(ctx, false, true); err != nil {
			return err
		}
		if err := p.Terraform.SetCCIP(ctx); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	p.Logger.Debug("Build and upload binary to CC")
	cmds := []string{
		// Build binary locally for DO droplet architecture.
		"CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o build/app ./node",

		// Upload binary to shared NFS directory in CC server.
		fmt.Sprintf("scp -C %s %s %s", sshOpts, "build/app", "root@"+p.Terraform.CCIP+":/data"),
	}
	if err := exec.Command(ctx, "/bin/sh", "-c", strings.Join(cmds, " && ")); err != nil {
		return err
	}

	return nil
}

// FIX: VPC is not deleted on first call to destroy:
// > Error deleting VPC: DELETE https://api.digitalocean.com/v2/vpcs/218fc123-585f-4e8f-97ec-a5bfa328fefe: 409 (request "33d60f7c-5ede-4aeb-bed6-74c58868d246") Can not delete VPC with members.
func (p *Provider) Cleanup(ctx context.Context, exceptCC, confirm bool) error {
	// TODO: exceptCC

	vars := []string{"testnet_dir=" + p.DataDir, "manifest_path=" + p.ManifestPath}
	if err := p.Terraform.Destroy(ctx, vars, exceptCC, confirm); err != nil {
		return err
	}
	return os.RemoveAll(p.DataDir)
}

// Setup uploads the generated config files to each node.
func (p *Provider) Setup(ctx context.Context, clean bool) error {
	for _, n := range p.Testnet.Nodes {
		if n.ClockSkew != 0 {
			return fmt.Errorf("node %q contains clock skew configuration (not supported on DO)", n.Name)
		}
	}

	remoteDir := "/cometbft"
	remoteFile := "config.tgz"

	// Upload config files to each node in parallel.
	p.Logger.Debug("Compress and upload generated config files to corresponding nodes")
	g, groupCtx := errgroup.WithContext(ctx)
	for _, n := range p.Testnet.Nodes {
		g.Go(func() error {
			nodeDir := filepath.Join(p.DataDir, n.Name)
			tgzFile := nodeDir + ".tgz"

			// Compress locally.
			if err := exec.Command(groupCtx, "tar", "-cvzf", tgzFile, "--directory", nodeDir, "."); err != nil {
				return fmt.Errorf("%s: %w", n.Name, err)
			}

			// Copy to remote.
			remoteLocation := "root@" + n.ExternalIP.String() + ":" + remoteDir + "/" + remoteFile
			cmd := fmt.Sprintf("scp -r %s %s %s", sshOpts, tgzFile, remoteLocation)
			if err := exec.Command(groupCtx, "/bin/sh", "-c", cmd); err != nil {
				return fmt.Errorf("%s: %w", n.Name, err)
			}

			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return fmt.Errorf("failed while deploying config files to %w", err)
	}

	p.Logger.Debug("Uncompress config files remotely", "clean", clean)
	nodeIPs := p.allNodeIPs()
	cmd := fmt.Sprintf("cd %s && tar -xvzf %s", remoteDir, remoteFile)
	if clean {
		cmd = fmt.Sprintf("rm -rdf %s/config && rm -rdf %s/data && %s", remoteDir, remoteDir, cmd)
	}
	err := pssh(ctx, nodeIPs, cmd)
	if err != nil {
		return err
	}

	if p.Testnet.LatencyEmulationEnabled {
		p.Logger.Debug("Run script to emulate latency remotely")
		if err := pssh(ctx, nodeIPs, "/cometbft/emulate-latency.sh"); err != nil {
			return err
		}
	}

	return nil
}

func (p *Provider) StartNodes(ctx context.Context, nodes ...*e2e.Node) error {
	nodeIPs := make([]string, len(nodes))
	for i, n := range nodes {
		nodeIPs[i] = n.ExternalIP.String()
	}
	p.Logger.Debug("Start app in nodes", "num", len(nodes))
	if err := pssh(ctx, nodeIPs, "systemctl start testappd"); err != nil {
		return err
	}
	// Defer logging because of delayed output from pssh.
	defer p.logMonitorInfo(ctx)
	return nil
}

func (p *Provider) StopTestnet(ctx context.Context, force bool) error {
	p.Logger.Debug("Stop app in nodes", "force", force)
	if force {
		return pssh(ctx, p.allNodeIPs(), "systemctl kill testappd")
	}
	return pssh(ctx, p.allNodeIPs(), "systemctl stop testappd")
}

func (*Provider) Disconnect(ctx context.Context, node *e2e.Node) error {
	cmds := []string{
		"iptables -A INPUT -p tcp --dport 26656 -j DROP",
		"iptables -A OUTPUT -p tcp --dport 26656 -j DROP",
	}
	return pssh(ctx, []string{node.ExternalIP.String()}, strings.Join(cmds, " && "))
}

func (*Provider) Reconnect(ctx context.Context, node *e2e.Node) error {
	cmds := []string{
		"iptables -D INPUT -p tcp --dport 26656 -j DROP",
		"iptables -D OUTPUT -p tcp --dport 26656 -j DROP",
	}
	return pssh(ctx, []string{node.ExternalIP.String()}, strings.Join(cmds, " && "))
}

func (*Provider) Logs(ctx context.Context, tail bool, node *e2e.Node) error {
	if node == nil {
		return errors.New("node name must be provided when using DO infra")
	}
	follow := ""
	if tail {
		follow = " -f"
	}
	return sshVerbose(ctx, node.ExternalIP.String(), "journalctl -u testappd"+follow)
}

func (*Provider) CheckUpgraded(_ context.Context, node *e2e.Node) (string, bool, error) {
	// Upgrade not supported yet by DO provider
	return node.Name, false, nil
}

func (Provider) NodeIP(node *e2e.Node) net.IP {
	return node.ExternalIP
}

// The IP addresses of all nodes in the testnet.
func (p *Provider) allNodeIPs() []string {
	ips := make([]string, len(p.Testnet.Nodes))
	for i, node := range p.Testnet.Nodes {
		ips[i] = node.ExternalIP.String()
	}
	return ips
}

func (p *Provider) logMonitorInfo(ctx context.Context) {
	if err := p.Terraform.SetCCIP(ctx); err != nil {
		return
	}
	p.Logger.Info("Grafana: http://" + p.Terraform.CCIP + ":3000 ; Prometheus: http://" + p.Terraform.CCIP + ":9090")
}

func psshCmd(ips []string, cmd string) string {
	hosts := strings.Join(ips, " ")
	return fmt.Sprintf("pssh -l root %s -i -v -p %d -t 120 -H \"%s\" \"%s\"", psshOpts, len(ips), hosts, cmd)
}

func pssh(ctx context.Context, ips []string, cmd string) error {
	if err := exec.Command(ctx, "/bin/sh", "-c", psshCmd(ips, cmd)); err != nil {
		return fmt.Errorf("pssh failed: %w", err)
	}
	return nil
}

func ssh(ctx context.Context, ip string, cmd string) error {
	return exec.Command(ctx, "/bin/sh", "-c", fmt.Sprintf("ssh %s root@%s \"%s\"", sshOpts, ip, cmd))
}

func sshVerbose(ctx context.Context, ip string, cmd string) error {
	return exec.CommandVerbose(ctx, "/bin/sh", "-c", fmt.Sprintf("ssh %s root@%s \"%s\"", sshOpts, ip, cmd))
}
