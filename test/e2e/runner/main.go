package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cometbft/cometbft/libs/log"
	e2e "github.com/cometbft/cometbft/test/e2e/pkg"
	"github.com/cometbft/cometbft/test/e2e/pkg/infra"
	"github.com/cometbft/cometbft/test/e2e/pkg/infra/digitalocean"
	"github.com/cometbft/cometbft/test/e2e/pkg/infra/docker"
)

const randomSeed = 2308084734268

const (
	infraTypeDocker       = "docker"
	infraTypeDigitalOcean = "digital-ocean"
	infraTypeDO           = "DO"
)

var logger = log.NewLogger(os.Stdout)

func main() {
	NewCLI().Run()
}

// CLI is the Cobra-based command-line interface.
type CLI struct {
	root         *cobra.Command
	dir          string
	manifestPath string
	manifest     *e2e.Manifest
	testnet      *e2e.Testnet
	preserve     bool
	inft         string
	infp         infra.Provider
}

// NewCLI sets up the CLI.
func NewCLI() *CLI {
	cli := &CLI{}
	cli.root = &cobra.Command{
		Use:           "runner",
		Short:         "End-to-end test runner",
		SilenceUsage:  true,
		SilenceErrors: true, // we'll output them ourselves in Run()
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			file, err := cmd.Flags().GetString("file")
			if err != nil {
				return err
			}
			cli.manifestPath = file

			if cli.manifestPath == "" {
				_ = cmd.Help()
				os.Exit(0)
			}

			manifest, err := e2e.LoadManifest(cli.manifestPath)
			if err != nil {
				return err
			}
			cli.manifest = &manifest

			dir, err := cmd.Flags().GetString("testnet-dir")
			if err != nil {
				return err
			}
			if dir == "" {
				// Set default testnet directory.
				dir = strings.TrimSuffix(file, filepath.Ext(file))
			}
			cli.dir = dir

			inft, err := cmd.Flags().GetString("infrastructure-type")
			if err != nil {
				return err
			}
			if inft == "" {
				// Set default infra type.
				inft = infraTypeDocker
			}
			cli.inft = inft

			return nil
		},
		PreRunE: cli.initInfraWithTestnet,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := cli.infp.Cleanup(cmd.Context(), false, false); err != nil {
				return err
			}
			if err := Setup(cmd.Context(), cli.testnet, cli.infp, false); err != nil {
				return err
			}

			r := rand.New(rand.NewSource(randomSeed)) //nolint: gosec

			chLoadResult := make(chan error)
			ctx, loadCancel := context.WithCancel(context.Background())
			defer loadCancel()
			go func() {
				err := Load(ctx, cli.testnet, false)
				if err != nil {
					logger.Error(fmt.Sprintf("Transaction load failed: %v", err.Error()))
				}
				chLoadResult <- err
			}()

			if err := Start(cmd.Context(), cli.testnet, cli.infp); err != nil {
				return err
			}

			if err := Wait(cmd.Context(), cli.testnet, 5); err != nil { // allow some txs to go through
				return err
			}

			if cli.testnet.HasPerturbations() {
				if err := Perturb(cmd.Context(), cli.testnet, cli.infp); err != nil {
					return err
				}
				if err := Wait(cmd.Context(), cli.testnet, 5); err != nil { // allow some txs to go through
					return err
				}
			}

			if cli.testnet.Evidence > 0 {
				if err := InjectEvidence(ctx, r, cli.testnet, cli.testnet.Evidence); err != nil {
					return err
				}
				if err := Wait(cmd.Context(), cli.testnet, 5); err != nil { // ensure chain progress
					return err
				}
			}

			loadCancel()
			if err := <-chLoadResult; err != nil {
				return err
			}
			if err := Wait(cmd.Context(), cli.testnet, 5); err != nil { // wait for network to settle before tests
				return err
			}
			if err := Test(cli.testnet, cli.infp.GetInfrastructureData()); err != nil {
				return err
			}
			if !cli.preserve {
				if err := cli.infp.Cleanup(cmd.Context(), false, false); err != nil {
					return err
				}
			}
			return nil
		},
	}

	cli.root.PersistentFlags().StringP("file", "f", "", "Testnet TOML manifest")
	_ = cli.root.MarkPersistentFlagRequired("file")

	cli.root.PersistentFlags().StringP("testnet-dir", "d", "",
		"Set the directory for the testnet files generated during setup.")

	cli.root.PersistentFlags().StringP("infrastructure-type", "t",
		"docker", "Backing infrastructure used to run the testnet. "+
			"Either 'digital-ocean' (equally 'DO') or 'docker'.")

	cli.root.PersistentFlags().StringP("infrastructure-data", "i", "",
		"Path to the json file containing infrastructure data (default '<testnet-dir>/infra-data.json'). "+
			"Only used if infra type is not 'docker' ")

	cli.root.Flags().BoolVarP(&cli.preserve, "preserve", "p", false,
		"Preserves the running of the test net after tests are completed.")

	infraCmd := cobra.Command{
		Use:   "infra",
		Short: "Manage remote infrastructure",
	}
	cli.root.AddCommand(&infraCmd)

	infraCmd.AddCommand(&cobra.Command{
		Use:     "init",
		Short:   "Initialize Terraform state and validate it (need to run only once).",
		PreRunE: cli.initRemoteInfraProvider,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// TODO: check that DO token exists/is valid.
			logger.Info("infra init", "type", cli.inft)
			return cli.infp.InfraInit(cmd.Context())
		},
	})

	infraCreateCmd := cobra.Command{
		Use:     "create",
		Short:   "Create and initialize remote nodes and/or Command & Control (CC) server.",
		PreRunE: cli.initRemoteInfraProvider,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ccOnly, err := cmd.Flags().GetBool("cc-only")
			if err != nil {
				return err
			}
			yes, err := cmd.Flags().GetBool("yes")
			if err != nil {
				return err
			}
			return InfraCreate(cmd.Context(), cli.infp, ccOnly, !yes)
		},
	}
	infraCreateCmd.PersistentFlags().BoolP("cc-only", "c", false, "Create Command & Control (CC) server only.")
	infraCreateCmd.PersistentFlags().Bool("yes", false, "Don't ask for confirmation.")
	infraCmd.AddCommand(&infraCreateCmd)

	infraCmd.AddCommand(&cobra.Command{
		Use:     "check",
		Short:   "Check status of Command & Control (CC) server.",
		PreRunE: cli.initInfraWithTestnet,
		RunE: func(cmd *cobra.Command, _ []string) error {
			logger.Info("infra check", "type", cli.inft)
			return cli.infp.InfraCheck(cmd.Context())
		},
	})

	infraDestroyCmd := cobra.Command{
		Use:     "destroy",
		Short:   "Destroy all resources.",
		PreRunE: cli.initRemoteInfraProvider,
		RunE: func(cmd *cobra.Command, _ []string) error {
			startTime := time.Now()
			defer func(t time.Time) { logger.Debug(fmt.Sprintf("Infra destroy time: %s\n", time.Since(t))) }(startTime)

			exceptCC, err := cmd.Flags().GetBool("except-cc")
			if err != nil {
				return err
			}
			yes, err := cmd.Flags().GetBool("yes")
			if err != nil {
				return err
			}
			return cli.infp.Cleanup(cmd.Context(), exceptCC, !yes)
		},
	}
	infraDestroyCmd.PersistentFlags().Bool("except-cc", true, "Destroy all nodes except CC server (only DO infra type).")
	infraDestroyCmd.PersistentFlags().Bool("yes", false, "Don't ask for confirmation.")
	infraCmd.AddCommand(&infraDestroyCmd)

	buildCmd := cobra.Command{
		Use:     "build",
		Short:   "Build Docker image with node binary and deploy to nodes",
		PreRunE: cli.initInfraWithTestnet,
		RunE: func(cmd *cobra.Command, _ []string) error {
			startTime := time.Now()
			defer func(t time.Time) { logger.Debug(fmt.Sprintf("Build time: %s\n", time.Since(t))) }(startTime)

			fast, err := cmd.Flags().GetBool("fast")
			if err != nil {
				return err
			}
			return cli.infp.Build(cmd.Context(), fast)
		},
	}
	buildCmd.PersistentFlags().Bool("fast", true, "Compile node binary outside the Docker image (only for 'docker' infra type).")
	cli.root.AddCommand(&buildCmd)

	setupCmd := cobra.Command{
		Use:     "setup",
		Short:   "Generates the testnet directory and configuration",
		PreRunE: cli.initInfraWithTestnet,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clean, err := cmd.Flags().GetBool("clean")
			if err != nil {
				return err
			}
			le, err := cmd.Flags().GetBool("le")
			if err != nil {
				return err
			}
			if !le {
				cli.testnet.LatencyEmulationEnabled = false
			}
			return Setup(cmd.Context(), cli.testnet, cli.infp, clean)
		},
	}
	setupCmd.PersistentFlags().Bool("clean", true, "Clean home directory before deploying the new config files.")
	// FIX: latency emulation should be run before setup, but currently it needs testnet.
	setupCmd.PersistentFlags().Bool("le", true, "Run latency emulation script.")
	cli.root.AddCommand(&setupCmd)

	cli.root.AddCommand(&cobra.Command{
		Use:     "start",
		Short:   "Starts the testnet, waiting for nodes to become available",
		PreRunE: cli.initInfraWithTestnet,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := os.Stat(cli.testnet.Dir)
			if errors.Is(err, fs.ErrNotExist) {
				err = Setup(cmd.Context(), cli.testnet, cli.infp, false)
			}
			if err != nil {
				return err
			}
			return Start(cmd.Context(), cli.testnet, cli.infp)
		},
	})

	cli.root.AddCommand(&cobra.Command{
		Use:     "perturb",
		Short:   "Perturbs the testnet, e.g. by restarting or disconnecting nodes",
		PreRunE: cli.initInfraWithTestnet,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return Perturb(cmd.Context(), cli.testnet, cli.infp)
		},
	})

	cli.root.AddCommand(&cobra.Command{
		Use:     "wait",
		Short:   "Waits for a few blocks to be produced and all nodes to catch up",
		PreRunE: cli.initInfraWithTestnet,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return Wait(cmd.Context(), cli.testnet, 5)
		},
	})

	stopCmd := cobra.Command{
		Use:     "stop",
		Short:   "Stops the testnet",
		PreRunE: cli.initInfraWithTestnet,
		RunE: func(cmd *cobra.Command, _ []string) error {
			startTime := time.Now()
			defer func(t time.Time) { logger.Debug(fmt.Sprintf("Stop time: %s\n", time.Since(t))) }(startTime)

			logger.Info("Stopping testnet")
			force, err := cmd.Flags().GetBool("force")
			if err != nil {
				return err
			}
			return cli.infp.StopTestnet(context.Background(), force)
		},
	}
	stopCmd.PersistentFlags().Bool("force", false, "Kill and restart Docker.")
	cli.root.AddCommand(&stopCmd)

	loadCmd := &cobra.Command{
		Use:     "load",
		Short:   "Generates transaction load until the command is canceled.",
		PreRunE: cli.initInfraWithTestnet,
		RunE: func(cmd *cobra.Command, _ []string) (err error) {
			useInternalIP, err := cmd.Flags().GetBool("internal-ip")
			if err != nil {
				return err
			}
			if loadRate, err := cmd.Flags().GetInt("rate"); err != nil {
				return err
			} else if loadRate > 0 {
				cli.testnet.LoadTxBatchSize = loadRate
			}
			if loadSize, err := cmd.Flags().GetInt("size"); err != nil {
				return err
			} else if loadSize > 0 {
				cli.testnet.LoadTxSizeBytes = loadSize
			}
			if loadConnections, err := cmd.Flags().GetInt("conn"); err != nil {
				return err
			} else if loadConnections > 0 {
				cli.testnet.LoadTxConnections = loadConnections
			}
			if loadTime, err := cmd.Flags().GetInt("time"); err != nil {
				return err
			} else if loadTime > 0 {
				cli.testnet.LoadMaxSeconds = loadTime
			}
			if loadTargetNodes, err := cmd.Flags().GetStringSlice("nodes"); err != nil {
				return err
			} else if len(loadTargetNodes) > 0 {
				cli.testnet.LoadTargetNodes = loadTargetNodes
			}
			if err = cli.testnet.Validate(); err != nil {
				return err
			}
			return Load(context.Background(), cli.testnet, useInternalIP)
		},
	}
	loadCmd.PersistentFlags().IntP("rate", "r", -1,
		"Number of transactions generate each second on all connections). Overwrites manifest option load_tx_batch_size.")
	loadCmd.PersistentFlags().IntP("size", "s", -1,
		"Transaction size in bytes. Overwrites manifest option load_tx_size_bytes.")
	loadCmd.PersistentFlags().IntP("conn", "c", -1,
		"Number of connections to open at each target node simultaneously. Overwrites manifest option load_tx_connections.")
	loadCmd.PersistentFlags().IntP("time", "T", -1,
		"Maximum duration (in seconds) of the load test. Overwrites manifest option load_max_seconds.")
	loadCmd.PersistentFlags().StringSliceP("nodes", "n", nil,
		"Comma-separated list of node names to send load to. Manifest option send_no_load will be ignored.")
	loadCmd.PersistentFlags().Bool("internal-ip", false,
		"Use nodes' internal IP addresses when sending transaction load. For running from inside a DO private network.")
	cli.root.AddCommand(loadCmd)

	cli.root.AddCommand(&cobra.Command{
		Use:     "evidence [amount]",
		Args:    cobra.MaximumNArgs(1),
		Short:   "Generates and broadcasts evidence to a random node",
		PreRunE: cli.initInfraWithTestnet,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			amount := 1

			if len(args) == 1 {
				amount, err = strconv.Atoi(args[0])
				if err != nil {
					return err
				}
			}

			return InjectEvidence(
				cmd.Context(),
				rand.New(rand.NewSource(randomSeed)), //nolint: gosec
				cli.testnet,
				amount,
			)
		},
	})

	cli.root.AddCommand(&cobra.Command{
		Use:     "test",
		Short:   "Runs test cases against a running testnet",
		PreRunE: cli.initInfraWithTestnet,
		RunE: func(_ *cobra.Command, _ []string) error {
			return Test(cli.testnet, cli.infp.GetInfrastructureData())
		},
	})

	monitorCmd := cobra.Command{
		Use:     "monitor",
		Aliases: []string{"mon"},
		Short:   "Manage local monitoring services such as Prometheus, Grafana, ElasticSearch, etc.",
		Long: "Manage monitoring services such as Prometheus, Grafana, ElasticSearch, etc.\n" +
			"First run 'setup' to generate a Prometheus config file.",
	}
	monitorStartCmd := cobra.Command{
		Use:     "start",
		Aliases: []string{"up"},
		Short:   "Start monitoring services.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := os.Stat(PrometheusConfigFile)
			if errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("file %s not found", PrometheusConfigFile)
			}
			if err := docker.ExecComposeVerbose(cmd.Context(), "monitoring", "up", "-d"); err != nil {
				return err
			}
			logger.Info("Grafana: http://localhost:3000 ; Prometheus: http://localhost:9090")
			return nil
		},
	}
	monitorStopCmd := cobra.Command{
		Use:     "stop",
		Aliases: []string{"down"},
		Short:   "Stop monitoring services.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := os.Stat(PrometheusConfigFile)
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			logger.Info("Shutting down monitoring services.")
			if err := docker.ExecComposeVerbose(cmd.Context(), "monitoring", "down"); err != nil {
				return err
			}
			// Remove prometheus config only when there is no testnet.
			if _, err := os.Stat(cli.dir); errors.Is(err, fs.ErrNotExist) {
				if err := os.RemoveAll(PrometheusConfigFile); err != nil {
					return err
				}
			}
			return nil
		},
	}
	monitorCmd.AddCommand(&monitorStartCmd)
	monitorCmd.AddCommand(&monitorStopCmd)
	cli.root.AddCommand(&monitorCmd)

	cleanupCmd := cobra.Command{
		Use:     "cleanup",
		Aliases: []string{"clean"},
		Short:   "Removes the testnet directory",
		PreRunE: cli.initInfraWithTestnet,
		RunE: func(cmd *cobra.Command, _ []string) error {
			startTime := time.Now()
			defer func(t time.Time) { logger.Debug(fmt.Sprintf("Cleanup time: %s\n", time.Since(t))) }(startTime)

			yes, err := cmd.Flags().GetBool("yes")
			if err != nil {
				return err
			}
			// Alert if monitoring services are still running.
			outBytes, err := docker.ExecComposeOutput(cmd.Context(), "monitoring", "ps", "--services", "--filter", "status=running")
			out := strings.TrimSpace(string(outBytes))
			if err == nil && len(out) != 0 {
				logger.Info("Monitoring services are still running:\n" + out)
			}
			return cli.infp.Cleanup(cmd.Context(), false, !yes)
		},
	}
	cleanupCmd.PersistentFlags().Bool("yes", false, "Don't ask for confirmation.")
	cli.root.AddCommand(&cleanupCmd)

	var splitLogs bool
	logCmd := &cobra.Command{
		Use:     "logs",
		Short:   "Shows the testnet logs. Use `--split` to split logs into separate files",
		PreRunE: cli.initInfraWithTestnet,
		RunE: func(cmd *cobra.Command, args []string) error {
			splitLogs, _ = cmd.Flags().GetBool("split")
			if splitLogs && cli.inft != infraTypeDocker {
				return errors.New("--split flag is only available for 'docker' infra type")
			}
			if splitLogs && len(args) > 0 {
				return errors.New("--split flag should be used without arguments")
			}
			if len(args) > 1 {
				return fmt.Errorf("too many node names provided: %s", args)
			}
			nodeName := ""
			if len(args) == 1 {
				nodeName = args[0]
			}
			if splitLogs {
				for _, node := range cli.testnet.Nodes {
					fmt.Println("Log for", node.Name)
					if err := cli.infp.Logs(cmd.Context(), false, node); err != nil {
						return err
					}
				}
				return nil
			}
			return cli.infp.Logs(cmd.Context(), false, cli.testnet.GetNodeByName(nodeName))
		},
	}
	logCmd.PersistentFlags().BoolVar(&splitLogs, "split", false, "outputs separate logs for each container")
	cli.root.AddCommand(logCmd)

	cli.root.AddCommand(&cobra.Command{
		Use:     "tail",
		Short:   "Tails the testnet logs",
		PreRunE: cli.initInfraWithTestnet,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return fmt.Errorf("too many node names provided: %s", args)
			}
			nodeName := ""
			if len(args) == 1 {
				nodeName = args[0]
			}
			return cli.infp.Logs(cmd.Context(), true, cli.testnet.GetNodeByName(nodeName))
		},
	})

	cli.root.AddCommand(&cobra.Command{
		Use:   "benchmark",
		Short: "Benchmarks testnet",
		Long: `Benchmarks the following metrics:
	Mean Block Interval
	Standard Deviation
	Min Block Interval
	Max Block Interval
over a 100 block sampling period.

Does not run any perturbations.
		`,
		PreRunE: cli.initInfraWithTestnet,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := cli.infp.Cleanup(cmd.Context(), false, false); err != nil {
				return err
			}
			if err := Setup(cmd.Context(), cli.testnet, cli.infp, false); err != nil {
				return err
			}

			chLoadResult := make(chan error)
			ctx, loadCancel := context.WithCancel(cmd.Context())
			defer loadCancel()
			go func() {
				err := Load(ctx, cli.testnet, false)
				if err != nil {
					logger.Error(fmt.Sprintf("Transaction load errored: %v", err.Error()))
				}
				chLoadResult <- err
			}()

			if err := Start(cmd.Context(), cli.testnet, cli.infp); err != nil {
				return err
			}

			if err := Wait(cmd.Context(), cli.testnet, 5); err != nil { // allow some txs to go through
				return err
			}

			// we benchmark performance over the next 100 blocks
			if err := Benchmark(cmd.Context(), cli.testnet, 100); err != nil {
				return err
			}

			loadCancel()
			if err := <-chLoadResult; err != nil {
				return err
			}

			return cli.infp.Cleanup(cmd.Context(), false, false)
		},
	})

	return cli
}

// Run runs the CLI.
func (cli *CLI) Run() {
	if err := cli.root.Execute(); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}

func (cli *CLI) initRemoteInfraProvider(_ *cobra.Command, _ []string) error {
	switch cli.inft {
	case infraTypeDocker:
		return errors.New("'docker' infra type not supported on this command")
	case infraTypeDigitalOcean, infraTypeDO:
		pd := infra.NewProviderData(cli.manifest, cli.manifestPath, cli.dir, nil, nil, logger)
		cli.infp = &digitalocean.Provider{ProviderData: pd}
	default:
		return fmt.Errorf("unknown infrastructure type '%s'", cli.inft)
	}
	return nil
}

func (cli *CLI) initInfraWithTestnet(cmd *cobra.Command, _ []string) error {
	var ifd e2e.InfrastructureData
	switch cli.inft {
	case infraTypeDocker:
		var err error
		ifd, err = e2e.NewDockerInfrastructureData(*cli.manifest)
		if err != nil {
			return err
		}
	case infraTypeDigitalOcean, infraTypeDO:
		path, err := cmd.Flags().GetString("infrastructure-data")
		if err != nil {
			return err
		}
		if path == "" {
			// Try with default path.
			path = filepath.Join(cli.dir, "infra-data.json")
		}
		_, err = os.Stat(path)
		if os.IsNotExist(err) {
			// TODO: call infra create?
			return errors.New("infrastructure data file not found")
		}
		ifd, err = e2e.InfrastructureDataFromFile(path)
		if err != nil {
			return fmt.Errorf("parsing infrastructure data: %s", err)
		}
	default:
		return fmt.Errorf("unknown infrastructure type '%s'", cli.inft)
	}

	testnet, err := e2e.LoadTestnet(cli.manifestPath, ifd, cli.dir)
	if err != nil {
		return fmt.Errorf("loading testnet: %s", err)
	}
	cli.testnet = testnet

	pd := infra.NewProviderData(cli.manifest, cli.manifestPath, cli.dir, testnet, &ifd, logger)
	switch cli.inft {
	case infraTypeDocker:
		cli.infp = &docker.Provider{ProviderData: pd}
	case infraTypeDigitalOcean, infraTypeDO:
		cli.infp = &digitalocean.Provider{ProviderData: pd}
	default:
		return fmt.Errorf("bad infrastructure type: %s", cli.inft)
	}
	return nil
}
