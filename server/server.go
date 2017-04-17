// package server contains the `pilosa server` subcommand which runs Pilosa
// itself. The purpose of this package is to define an easily tested Command
// object which handles interpreting configuration and setting up all the
// objects that Pilosa needs.
package server

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pilosa/pilosa"
)

func init() {
	rand.Seed(time.Now().UTC().UnixNano())
}

const (
	// DefaultDataDir is the default data directory.
	DefaultDataDir = "~/.pilosa"
)

// Command represents the state of the pilosa server command.
type Command struct {
	Server *pilosa.Server

	// Configuration.
	Config *pilosa.Config

	// Profiling options.
	CPUProfile string
	CPUTime    time.Duration

	// Standard input/output
	*pilosa.CmdIO

	// running will be closed once Command.Run is finished.
	Started chan struct{}
	// Done will be closed when Command.Close() is called
	Done chan struct{}
}

// NewMain returns a new instance of Main.
func NewCommand(stdin io.Reader, stdout, stderr io.Writer) *Command {
	return &Command{
		Server: pilosa.NewServer(),
		Config: pilosa.NewConfig(),

		CmdIO: pilosa.NewCmdIO(stdin, stdout, stderr),

		Started: make(chan struct{}),
		Done:    make(chan struct{}),
	}
}

// Run executes the pilosa server.
func (m *Command) Run(args ...string) (err error) {
	defer close(m.Started)
	prefix := "~" + string(filepath.Separator)
	if strings.HasPrefix(m.Config.DataDir, prefix) {
		HomeDir := os.Getenv("HOME")
		if HomeDir == "" {
			return errors.New("data directory not specified and no home dir available")
		}
		m.Config.DataDir = filepath.Join(HomeDir, strings.TrimPrefix(m.Config.DataDir, prefix))
	}

	// Setup logging output.
	if m.Config.LogPath == "" {
		m.Server.LogOutput = m.Stderr
	} else {
		logFile, err := os.OpenFile(m.Config.LogPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			return err
		}
		m.Server.LogOutput = logFile
	}

	// Configure index.
	fmt.Fprintf(m.Stderr, "Using data from: %s\n", m.Config.DataDir)
	m.Server.Index.Path = m.Config.DataDir
	m.Server.Index.Stats = pilosa.NewExpvarStatsClient()

	// Build cluster from config file.
	m.Server.Host, err = normalizeHost(m.Config.Host)
	if err != nil {
		return err
	}
	m.Server.Broadcaster = PilosaBroadcaster(m.Config, m.Server)
	m.Server.Cluster = PilosaCluster(m.Config)

	// Associate objects to the Broadcaster based on config.
	AssociateBroadcaster(m.Server, m.Config)

	// Set configuration options.
	m.Server.AntiEntropyInterval = time.Duration(m.Config.AntiEntropy.Interval)

	// Initialize server.
	if err = m.Server.Open(); err != nil {
		return fmt.Errorf("server.Open: %v", err)
	}
	fmt.Fprintf(m.Stderr, "Listening as http://%s\n", m.Server.Host)
	return nil
}

// PilosaBroadcaster returns a new instance of Broadcaster based on the config.
func PilosaBroadcaster(c *pilosa.Config, server *pilosa.Server) (broadcaster pilosa.Broadcaster) {
	switch c.Cluster.BroadcasterType {
	case "http":
		broadcaster = pilosa.NewHTTPBroadcaster(server)
	case "gossip":
		broadcaster = pilosa.NewGossipBroadcaster(server)
	case "static":
		broadcaster = pilosa.NopBroadcaster
	}
	return broadcaster
}

// PilosaCluster returns a new instance of Cluster based on the config.
func PilosaCluster(c *pilosa.Config) *pilosa.Cluster {
	cluster := pilosa.NewCluster()
	cluster.ReplicaN = c.Cluster.ReplicaN

	for _, hostport := range c.Cluster.Nodes {
		cluster.Nodes = append(cluster.Nodes, &pilosa.Node{Host: hostport})
	}

	// Setup a Broadcast (over HTTP) or Gossip NodeSet based on config.
	switch c.Cluster.BroadcasterType {
	case "http":
		cluster.NodeSet = pilosa.NewHTTPNodeSet()
		cluster.NodeSet.(*pilosa.HTTPNodeSet).Join(cluster.Nodes)
	case "gossip":
		gport, err := strconv.Atoi(pilosa.DefaultGossipPort)
		if err != nil {
			panic(err) // Atoi on a compile-time constant should never fail.
		}
		gossipPort := gport
		gossipSeed := pilosa.DefaultHost
		if c.Cluster.Gossip.Port != 0 {
			gossipPort = c.Cluster.Gossip.Port
		}
		if c.Cluster.Gossip.Seed != "" {
			gossipSeed = c.Cluster.Gossip.Seed
		}
		// get the host portion of addr to use for binding
		gossipHost, _, err := net.SplitHostPort(c.Host)
		if err != nil {
			gossipHost = c.Host
		}
		cluster.NodeSet = pilosa.NewGossipNodeSet(c.Host, gossipHost, gossipPort, gossipSeed)
	case "static":
		cluster.NodeSet = pilosa.NewStaticNodeSet()
	default:
		cluster.NodeSet = pilosa.NewStaticNodeSet()
	}

	return cluster
}

// AssociateBroadcaster allows an implementation to associate objects to the Broadcaster
// after cluster configuration.
func AssociateBroadcaster(s *pilosa.Server, c *pilosa.Config) {
	switch c.Cluster.BroadcasterType {
	case "http":
		// nop
	case "gossip":
		s.Cluster.NodeSet.(*pilosa.GossipNodeSet).AttachBroadcaster(s.Broadcaster.(*pilosa.GossipBroadcaster))
	case "static":
		// nop
	}
}

func normalizeHost(host string) (string, error) {
	if !strings.Contains(host, ":") {
		host = host + ":"
	} else if strings.Contains(host, "://") {
		if strings.HasPrefix(host, "http://") {
			host = host[7:]
		} else {
			return "", fmt.Errorf("invalid scheme or host: '%s'. use the format [http://]<host>:<port>", host)
		}
	}
	return host, nil
}

// Close shuts down the server.
func (m *Command) Close() error {
	var logErr error
	serveErr := m.Server.Close()
	logOutput := m.Server.LogOutput
	if closer, ok := logOutput.(io.Closer); ok {
		logErr = closer.Close()
	}
	close(m.Done)
	if serveErr != nil && logErr != nil {
		return fmt.Errorf("closing server: '%v', closing logs: '%v'", serveErr, logErr)
	} else if logErr != nil {
		return logErr
	}
	return serveErr
}
