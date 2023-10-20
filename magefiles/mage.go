package main

import (
	"context"
	"os"

	"github.com/elisasre/mageutil"
	"github.com/magefile/mage/mg"
)

const (
	AppName   = "smokeping-prober"
	RepoURL   = "https://github.com/elisasre/smokeping_prober"
	ImageName = "valkama.saunalahti.fi/sre/smokeping_prober"
)

type (
	Go        mg.Namespace
	Docker    mg.Namespace
	Workspace mg.Namespace
)

// Build builds binaries for executables under ./cmd
func (Go) Build(ctx context.Context) error {
	return mageutil.BuildAll(ctx)
}

// UnitTest runs unit tests for whole repo
func (Go) UnitTest(ctx context.Context) error {
	return mageutil.UnitTest(ctx)
}

// IntegrationTest runs integration tests for whole repo
func (Go) IntegrationTest(ctx context.Context) error {
	return mageutil.IntegrationTest(ctx, "./cmd/"+AppName)
}

// Run runs the binary for smokeping-prober
func (Go) Run(ctx context.Context) error {
	return mageutil.Run(ctx, AppName, "--loglevel", "0", "--development", "true")
}

// Lint runs lint for all go files
func (Go) Lint(ctx context.Context) error {
	return mageutil.LintAll(ctx)
}

// VulnCheck runs vuln check for all go packages
func (Go) VulnCheck(ctx context.Context) error {
	return mageutil.VulnCheckAll(ctx)
}

// LicenseCheck runs license check for all go packages
func (Go) LicenseCheck(ctx context.Context) error {
	return mageutil.LicenseCheck(ctx, os.Stdout, mageutil.CmdDir+AppName)
}

// Image creates docker image
func (Docker) Image(ctx context.Context) error {
	return mageutil.DockerBuildDefault(ctx, ImageName, RepoURL)
}

// PushImage pushes docker image
func (Docker) PushImage(ctx context.Context) error {
	return mageutil.DockerPushAllTags(ctx, ImageName)
}

// Clean removes all files ignored by git
func (Workspace) Clean(ctx context.Context) error {
	return mageutil.Clean(ctx)
}
