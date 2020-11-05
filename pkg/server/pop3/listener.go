package pop3

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/inbucket/inbucket/pkg/config"
	"github.com/inbucket/inbucket/pkg/storage"
	"github.com/rs/zerolog/log"
)

// Server defines an instance of the POP3 server.
type Server struct {
	config         config.POP3
	address        string
	addressType    string
	store          storage.Store   // Mail store.
	listener       net.Listener    // TCP listener.
	globalShutdown chan bool       // Inbucket shutdown signal.
	wg             *sync.WaitGroup // Waitgroup tracking sessions.
}

// New creates a new Server struct.
func New(config config.POP3, address string, addressType string, shutdownChan chan bool, store storage.Store) *Server {
	return &Server{
		config:         config,
		address:        address,
		addressType:    addressType,
		store:          store,
		globalShutdown: shutdownChan,
		wg:             new(sync.WaitGroup),
	}
}

// Start the server and listen for connections
func (s *Server) Start(ctx context.Context) {
	slog := log.With().Str("module", "pop3").Str("phase", "startup").Logger()
	addr, err := net.ResolveTCPAddr(s.addressType, s.address)
	if err != nil {
		slog.Error().Err(err).Msg("Failed to build " + s.addressType + " address")
		s.emergencyShutdown()
		return
	}
	slog.Info().Str("addr", addr.String()).Msg("POP3 listening on " + s.addressType)
	s.listener, err = net.ListenTCP("tcp", addr)
	if err != nil {
		slog.Error().Err(err).Msg("Failed to start " + s.addressType + " listener")
		s.emergencyShutdown()
		return
	}
	// Listener go routine.
	go s.serve(ctx)
	// Wait for shutdown.
	select {
	case _ = <-ctx.Done():
	}
	slog = log.With().Str("module", "pop3").Str("phase", "shutdown").Logger()
	slog.Debug().Msg("POP3 shutdown requested, connections will be drained")
	// Closing the listener will cause the serve() go routine to exit.
	if err := s.listener.Close(); err != nil {
		slog.Error().Err(err).Msg("Failed to close POP3 listener")
	}
}

// serve is the listen/accept loop.
func (s *Server) serve(ctx context.Context) {
	// Handle incoming connections.
	var tempDelay time.Duration
	for sid := 1; ; sid++ {
		if conn, err := s.listener.Accept(); err != nil {
			if nerr, ok := err.(net.Error); ok && nerr.Temporary() {
				// Temporary error, sleep for a bit and try again.
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				log.Error().Str("module", "pop3").Err(err).
					Msgf("POP3 accept error; retrying in %v", tempDelay)
				time.Sleep(tempDelay)
				continue
			} else {
				// Permanent error.
				select {
				case <-ctx.Done():
					// POP3 is shutting down.
					return
				default:
					// Something went wrong.
					s.emergencyShutdown()
					return
				}
			}
		} else {
			tempDelay = 0
			s.wg.Add(1)
			go s.startSession(sid, conn)
		}
	}
}

func (s *Server) emergencyShutdown() {
	// Shutdown Inbucket
	select {
	case _ = <-s.globalShutdown:
	default:
		close(s.globalShutdown)
	}
}

// Drain causes the caller to block until all active POP3 sessions have finished
func (s *Server) Drain() {
	// Wait for sessions to close
	s.wg.Wait()
	log.Debug().Str("module", "pop3").Str("phase", "shutdown").Msg("POP3 connections have drained")
}
