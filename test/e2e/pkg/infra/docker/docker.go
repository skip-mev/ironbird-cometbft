package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"text/template"

	"github.com/cometbft/cometbft/libs/log"
	e2e "github.com/cometbft/cometbft/test/e2e/pkg"
	"github.com/cometbft/cometbft/test/e2e/pkg/exec"
	"github.com/cometbft/cometbft/test/e2e/pkg/infra"
)

const DockerComposeFile = "compose.yaml"

var _ infra.Provider = (*Provider)(nil)

// Provider implements a docker-compose backed infrastructure provider.
type Provider struct {
	infra.ProviderData
}

func (*Provider) InfraInit(_ context.Context) error {
	return nil
}

func (*Provider) InfraCreate(_ context.Context, _ bool, _ bool) error {
	return nil
}

func (*Provider) InfraCheck(_ context.Context) error {
	return nil
}

func (p *Provider) Build(ctx context.Context, fast bool) error {
	tag := "cometbft/e2e-node:local-version"
	if fast {
		p.Logger.Info("Compile node binary for slim Docker image")
		if err := exec.CommandVerbose(ctx, "/bin/sh", "-c", "CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o build/node ./node"); err != nil {
			return err
		}
		p.Logger.Info("Build slim Docker image")
		return ExecVerbose(ctx, "build", "--tag", tag, "-f", "docker/Dockerfile.fast", ".")
	}

	p.Logger.Info("Build node image")
	return ExecVerbose(ctx, "build", "--tag", tag, "-f", "docker/Dockerfile", "../..")
}

func (p *Provider) Cleanup(_ context.Context, _ bool, _ bool) error {
	err := p.cleanupDocker()
	if err != nil {
		return err
	}
	err = p.cleanupDir(p.DataDir)
	if err != nil {
		return err
	}
	return nil
}

// Setup generates the docker-compose file and write it to disk, erroring if
// any of these operations fail.
func (p *Provider) Setup(_ context.Context, _ bool) error {
	compose, err := dockerComposeBytes(p.Testnet)
	if err != nil {
		return err
	}
	//nolint: gosec // G306: Expect WriteFile permissions to be 0600 or less
	err = os.WriteFile(filepath.Join(p.Testnet.Dir, DockerComposeFile), compose, 0o644)
	if err != nil {
		return err
	}
	return nil
}

func (p Provider) StartNodes(ctx context.Context, nodes ...*e2e.Node) error {
	nodeNames := make([]string, len(nodes))
	for i, n := range nodes {
		nodeNames[i] = n.Name
	}
	return ExecCompose(ctx, p.Testnet.Dir, append([]string{"up", "-d"}, nodeNames...)...)
}

func (p Provider) StopTestnet(ctx context.Context, _ bool) error {
	return ExecCompose(ctx, p.Testnet.Dir, "down")
}

func (p Provider) Disconnect(ctx context.Context, node *e2e.Node) error {
	return Exec(ctx, "network", "disconnect", p.Testnet.Name+"_"+p.Testnet.Name, node.Name)
}

func (p Provider) Reconnect(ctx context.Context, node *e2e.Node) error {
	return Exec(ctx, "network", "connect", p.Testnet.Name+"_"+p.Testnet.Name, node.Name)
}

func (p Provider) Logs(ctx context.Context, tail bool, node *e2e.Node) error {
	if node != nil {
		if tail {
			return ExecComposeVerbose(ctx, p.DataDir, "logs", "--follow", node.Name)
		}
		return ExecComposeVerbose(ctx, p.DataDir, "logs", node.Name)
	} else if tail {
		return ExecComposeVerbose(ctx, p.DataDir, "logs", "--follow")
	}
	return ExecComposeVerbose(ctx, p.DataDir, "logs")
}

func (Provider) CheckUpgraded(ctx context.Context, node *e2e.Node) (string, bool, error) {
	testnet := node.Testnet
	out, err := ExecComposeOutput(ctx, testnet.Dir, "ps", "-q", node.Name)
	if err != nil {
		return "", false, err
	}
	name := node.Name
	upgraded := false
	if len(out) == 0 {
		name += "_u"
		upgraded = true
	}
	return name, upgraded, nil
}

func (Provider) NodeIP(node *e2e.Node) net.IP {
	return node.InternalIP
}

// cleanupDocker removes all E2E resources (with label e2e=True), regardless
// of testnet.
func (p Provider) cleanupDocker() error {
	p.Logger.Info("Removing Docker containers and networks")

	// GNU xargs requires the -r flag to not run when input is empty, macOS
	// does this by default. Ugly, but works.
	xargsR := `$(if [[ $OSTYPE == "linux-gnu"* ]]; then echo -n "-r"; fi)`

	err := exec.Command(context.Background(), "bash", "-c", fmt.Sprintf(
		"docker container ls -qa --filter label=e2e | xargs %v docker container rm -f", xargsR))
	if err != nil {
		return err
	}

	err = exec.Command(context.Background(), "bash", "-c", fmt.Sprintf(
		"docker network ls -q --filter label=e2e | xargs %v docker network rm", xargsR))
	if err != nil {
		return err
	}

	return nil
}

// cleanupDir cleans up a testnet directory.
func (p Provider) cleanupDir(dir string) error {
	if dir == "" {
		return errors.New("no directory set")
	}

	_, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}

	p.Logger.Info("cleanup dir", "msg", log.NewLazySprintf("Removing testnet directory %#q", dir))

	// On Linux, some local files in the volume will be owned by root since CometBFT
	// runs as root inside the container, so we need to clean them up from within a
	// container running as root too.
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	err = Exec(context.Background(), "run", "--rm", "--entrypoint", "", "-v", fmt.Sprintf("%v:/network", absDir),
		"cometbft/e2e-node:local-version", "sh", "-c", "rm -rf /network/*/")
	if err != nil {
		return err
	}

	err = os.RemoveAll(dir)
	if err != nil {
		return err
	}

	return nil
}

// dockerComposeBytes generates a Docker Compose config file for a testnet and returns the
// file as bytes to be written out to disk.
func dockerComposeBytes(testnet *e2e.Testnet) ([]byte, error) {
	// Must use version 2 Docker Compose format, to support IPv6.
	tmpl, err := template.New("docker-compose").Parse(`
networks:
  {{ .Name }}:
    labels:
      e2e: true
    driver: bridge
{{- if .IPv6 }}
    enable_ipv6: true
{{- end }}
    ipam:
      driver: default
      config:
      - subnet: {{ .IP }}

services:
{{- range .Nodes }}
  {{ .Name }}:
    labels:
      e2e: true
    container_name: {{ .Name }}
    image: {{ .Version }}
{{- if or (eq .ABCIProtocol "builtin") (eq .ABCIProtocol "builtin_connsync") }}
    entrypoint: /usr/bin/entrypoint-builtin
{{- end }}
{{- if .ClockSkew }}
    environment:
        - COMETBFT_CLOCK_SKEW={{ .ClockSkew }}
{{- end }}
    cap_add:
      - NET_ADMIN
    init: true
    ports:
    - 26656
    - {{ if .RPCProxyPort }}{{ .RPCProxyPort }}:{{ end }}26657
    - {{ if .GRPCProxyPort }}{{ .GRPCProxyPort }}:{{ end }}26670
    - {{ if .GRPCPrivilegedProxyPort }}{{ .GRPCPrivilegedProxyPort }}:{{ end }}26671
{{- if .PrometheusProxyPort }}
    - {{ .PrometheusProxyPort }}:26660
{{- end }}
    - 6060
    - 2345
    - 2346
    volumes:
    - ./{{ .Name }}:/cometbft
    networks:
      {{ $.Name }}:
        ipv{{ if $.IPv6 }}6{{ else }}4{{ end}}_address: {{ .InternalIP }}
{{- if ne .Version $.UpgradeVersion}}

  {{ .Name }}_u:
    labels:
      e2e: true
    container_name: {{ .Name }}_u
    image: {{ $.UpgradeVersion }}
{{- if or (eq .ABCIProtocol "builtin") (eq .ABCIProtocol "builtin_connsync") }}
    entrypoint: /usr/bin/entrypoint-builtin
{{- end }}
{{- if .ClockSkew }}
    environment:
        - COMETBFT_CLOCK_SKEW={{ .ClockSkew }}
{{- end }}
    cap_add:
      - NET_ADMIN
    init: true
    ports:
    - 26656
    - {{ if .RPCProxyPort }}{{ .RPCProxyPort }}:{{ end }}26657
    - {{ if .GRPCProxyPort }}{{ .GRPCProxyPort }}:{{ end }}26670
    - {{ if .GRPCPrivilegedProxyPort }}{{ .GRPCPrivilegedProxyPort }}:{{ end }}26671
{{- if .PrometheusProxyPort }}
    - {{ .PrometheusProxyPort }}:26660
{{- end }}
    - 6060
    - 2345
    - 2346
    volumes:
    - ./{{ .Name }}:/cometbft
    networks:
      {{ $.Name }}:
        ipv{{ if $.IPv6 }}6{{ else }}4{{ end}}_address: {{ .InternalIP }}
{{- end }}

{{end}}`)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, testnet)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ExecCompose runs a Docker Compose command for a testnet.
func ExecCompose(ctx context.Context, dir string, args ...string) error {
	return exec.Command(ctx, append(
		[]string{"docker", "compose", "-f", filepath.Join(dir, DockerComposeFile)},
		args...)...)
}

// ExecComposeOutput runs a Docker Compose command for a testnet and returns the command's output.
func ExecComposeOutput(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return exec.CommandOutput(ctx, append(
		[]string{"docker", "compose", "-f", filepath.Join(dir, DockerComposeFile)},
		args...)...)
}

// ExecComposeVerbose runs a Docker Compose command for a testnet and displays its output.
func ExecComposeVerbose(ctx context.Context, dir string, args ...string) error {
	return exec.CommandVerbose(ctx, append(
		[]string{"docker", "compose", "-f", filepath.Join(dir, DockerComposeFile)},
		args...)...)
}

// Exec runs a Docker command.
func Exec(ctx context.Context, args ...string) error {
	return exec.Command(ctx, append([]string{"docker"}, args...)...)
}

// ExecVerbose runs a Docker command while displaying its output.
func ExecVerbose(ctx context.Context, args ...string) error {
	return exec.CommandVerbose(ctx, append([]string{"docker"}, args...)...)
}
