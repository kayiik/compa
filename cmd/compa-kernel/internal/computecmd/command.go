package computecmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"github.com/kayiik/compa/pkg/compute"
	"github.com/kayiik/compa/pkg/config"
)

// NewComputeCommand groups commands run on a machine that serves Compitas.
func NewComputeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compute",
		Short: "Use this machine as compute for Compitas",
	}
	cmd.AddCommand(newEnrollCommand(), newServeCommand(), newApproverCommand())
	return cmd
}

// newApproverCommand is the process hook a Compita's kernel starts to have its
// owner asked about tool calls (see compute.ServeApprover). Not for people.
func newApproverCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "approver",
		Short:  "Process hook that routes a Compita's approvals to its owner",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			return compute.ServeApprover(ctx, os.Stdin, os.Stdout, compute.HostAskerFromEnv().Ask)
		},
	}
}

func newEnrollCommand() *cobra.Command {
	var url, token string
	cmd := &cobra.Command{
		Use:   "enroll",
		Short: "Enroll this machine with a Compa using a one-time token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			caps := compute.Detect()
			id, err := compute.Enroll(ctx, nil, url, token, caps)
			if err != nil {
				return err
			}
			if err := compute.SaveIdentity(config.GetHome(), id); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Enrolled as %q with %s (docker: %v, browser: %v)\n", id.ComputeID, id.CompaURL, caps.Docker, caps.Browser)
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "Address of the Compa that issued the token")
	cmd.Flags().StringVar(&token, "token", "", "One-time enrollment token")
	_ = cmd.MarkFlagRequired("url")
	_ = cmd.MarkFlagRequired("token")
	return cmd
}

func newServeCommand() *cobra.Command {
	var image, listen, secret string
	var useTLS bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run Compitas for the Compa this machine enrolled with",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home := config.GetHome()
			if listen != "" {
				return serveDialIn(cmd, home, listen, secret, image, useTLS)
			}
			id, err := compute.LoadIdentity(home)
			if err != nil {
				return fmt.Errorf("this machine is not enrolled (run `compa-kernel compute enroll` first): %w", err)
			}
			kernel, err := os.Executable()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			fmt.Fprintf(cmd.OutOrStdout(), "Serving Compitas for %s as %q. Ctrl+C to stop.\n", id.CompaURL, id.ComputeID)
			compute.Serve(ctx, id, compute.Runner{Home: home, Kernel: kernel, Image: image}, func(f string, a ...any) {
				fmt.Fprintf(cmd.ErrOrStderr(), f+"\n", a...)
			})
			return nil
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "", "Dial-in mode: address to listen on, such as :8787")
	cmd.Flags().StringVar(&secret, "secret", "", "Dial-in mode: the secret Compa presents")
	cmd.Flags().BoolVar(&useTLS, "tls", false, "Dial-in mode: serve HTTPS with a self-signed certificate and print its fingerprint")
	cmd.Flags().StringVar(&image, "image", compute.DefaultImage, "Container image for container isolation")
	return cmd
}

func serveDialIn(cmd *cobra.Command, home, listen, secret, image string, useTLS bool) error {
	if secret == "" {
		return fmt.Errorf("--secret is required with --listen")
	}
	kernel, err := os.Executable()
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              listen,
		Handler:           compute.DialInHandler(secret, compute.Runner{Home: home, Kernel: kernel, Image: image}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	if useTLS {
		cert, fp, err := compute.LoadOrCreateCert(home)
		if err != nil {
			return err
		}
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		fmt.Fprintf(cmd.OutOrStdout(), "HTTPS with a self-signed certificate. Give Compa this fingerprint to pin: %s\n", fp)
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	fmt.Fprintf(cmd.OutOrStdout(), "Serving Compitas for Compa on %s. Put it behind HTTPS or a private network. Ctrl+C to stop.\n", listen)
	serve := srv.ListenAndServe
	if useTLS {
		serve = func() error { return srv.ListenAndServeTLS("", "") }
	}
	if err := serve(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
