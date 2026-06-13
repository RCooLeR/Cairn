package lineage

import (
	"testing"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestParseDockerfileFixtures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name              string
		dockerfile        string
		args              map[string]string
		target            string
		wantBases         []string
		wantExternal      []string
		wantFinalExternal string
		wantUnresolved    []string
		wantWarnings      int
		wantPlatform      string
		wantPinned        bool
		wantScratch       bool
		wantInternal      bool
	}{
		{name: "simple", dockerfile: "FROM nginx:alpine", wantBases: []string{"nginx:alpine"}, wantExternal: []string{"nginx:alpine"}, wantFinalExternal: "nginx:alpine"},
		{name: "lowercase instruction", dockerfile: "from alpine:3.20", wantBases: []string{"alpine:3.20"}, wantExternal: []string{"alpine:3.20"}, wantFinalExternal: "alpine:3.20"},
		{name: "bom and comment", dockerfile: "\ufeff# syntax=docker/dockerfile:1\nFROM busybox:1.36", wantBases: []string{"busybox:1.36"}, wantExternal: []string{"busybox:1.36"}, wantFinalExternal: "busybox:1.36"},
		{name: "line continuation", dockerfile: "FROM \\\n  redis:7-alpine", wantBases: []string{"redis:7-alpine"}, wantExternal: []string{"redis:7-alpine"}, wantFinalExternal: "redis:7-alpine"},
		{name: "named stage", dockerfile: "FROM golang:1.26 AS builder", wantBases: []string{"golang:1.26"}, wantExternal: []string{"golang:1.26"}, wantFinalExternal: "golang:1.26"},
		{name: "mixed case as", dockerfile: "FROM node:22 aS assets", wantBases: []string{"node:22"}, wantExternal: []string{"node:22"}, wantFinalExternal: "node:22"},
		{name: "multi final last", dockerfile: "FROM alpine:3.20 AS builder\nFROM nginx:alpine AS runtime", wantBases: []string{"alpine:3.20", "nginx:alpine"}, wantExternal: []string{"alpine:3.20", "nginx:alpine"}, wantFinalExternal: "nginx:alpine"},
		{name: "multi target by name", dockerfile: "FROM alpine:3.20 AS builder\nFROM nginx:alpine AS runtime", target: "builder", wantBases: []string{"alpine:3.20", "nginx:alpine"}, wantExternal: []string{"alpine:3.20", "nginx:alpine"}, wantFinalExternal: "alpine:3.20"},
		{name: "multi target by index", dockerfile: "FROM alpine:3.20 AS builder\nFROM nginx:alpine AS runtime", target: "1", wantBases: []string{"alpine:3.20", "nginx:alpine"}, wantExternal: []string{"alpine:3.20", "nginx:alpine"}, wantFinalExternal: "nginx:alpine"},
		{name: "missing target falls back", dockerfile: "FROM alpine AS a\nFROM debian AS b", target: "missing", wantBases: []string{"alpine", "debian"}, wantExternal: []string{"alpine", "debian"}, wantFinalExternal: "debian"},
		{name: "arg default braces", dockerfile: "ARG BASE=alpine:3.20\nFROM ${BASE}", wantBases: []string{"alpine:3.20"}, wantExternal: []string{"alpine:3.20"}, wantFinalExternal: "alpine:3.20"},
		{name: "arg override", dockerfile: "ARG BASE=alpine:3.20\nFROM ${BASE}", args: map[string]string{"BASE": "ubuntu:24.04"}, wantBases: []string{"ubuntu:24.04"}, wantExternal: []string{"ubuntu:24.04"}, wantFinalExternal: "ubuntu:24.04"},
		{name: "arg dollar", dockerfile: "ARG BASE=postgres:16-alpine\nFROM $BASE", wantBases: []string{"postgres:16-alpine"}, wantExternal: []string{"postgres:16-alpine"}, wantFinalExternal: "postgres:16-alpine"},
		{name: "unresolved arg", dockerfile: "FROM ${MISSING}:latest", wantBases: []string{"${MISSING}:latest"}, wantExternal: []string{"${MISSING}:latest"}, wantFinalExternal: "${MISSING}:latest", wantUnresolved: []string{"MISSING"}},
		{name: "arg without default unresolved", dockerfile: "ARG BASE\nFROM $BASE", wantBases: []string{"$BASE"}, wantExternal: []string{"$BASE"}, wantFinalExternal: "$BASE", wantUnresolved: []string{"BASE"}},
		{name: "platform equals", dockerfile: "FROM --platform=$BUILDPLATFORM golang:1.26 AS build", wantBases: []string{"golang:1.26"}, wantExternal: []string{"golang:1.26"}, wantFinalExternal: "golang:1.26", wantPlatform: "$BUILDPLATFORM"},
		{name: "platform separate", dockerfile: "FROM --platform linux/arm64 alpine:3.20", wantBases: []string{"alpine:3.20"}, wantExternal: []string{"alpine:3.20"}, wantFinalExternal: "alpine:3.20", wantPlatform: "linux/arm64"},
		{name: "scratch", dockerfile: "FROM scratch", wantBases: []string{"scratch"}, wantScratch: true},
		{name: "digest pinned", dockerfile: "FROM alpine@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", wantBases: []string{"alpine@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, wantExternal: []string{"alpine@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, wantFinalExternal: "alpine@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", wantPinned: true},
		{name: "previous stage internal", dockerfile: "FROM alpine AS builder\nFROM builder AS final", wantBases: []string{"alpine", "builder"}, wantExternal: []string{"alpine"}, wantFinalExternal: "alpine", wantInternal: true},
		{name: "previous numeric stage internal", dockerfile: "FROM alpine\nFROM 0 AS final", wantBases: []string{"alpine", "0"}, wantExternal: []string{"alpine"}, wantFinalExternal: "alpine", wantInternal: true},
		{name: "inline comment", dockerfile: "FROM nginx:alpine # runtime image", wantBases: []string{"nginx:alpine"}, wantExternal: []string{"nginx:alpine"}, wantFinalExternal: "nginx:alpine"},
		{name: "quoted arg default", dockerfile: "ARG BASE=\"node:22-alpine\"\nFROM $BASE", wantBases: []string{"node:22-alpine"}, wantExternal: []string{"node:22-alpine"}, wantFinalExternal: "node:22-alpine"},
		{name: "invalid from", dockerfile: "FROM", wantWarnings: 2},
		{name: "no from", dockerfile: "RUN echo hi", wantWarnings: 1},
		{name: "multiple arg fields", dockerfile: "ARG DIST=alpine VERSION=3.20\nFROM ${DIST}:${VERSION}", wantBases: []string{"alpine:3.20"}, wantExternal: []string{"alpine:3.20"}, wantFinalExternal: "alpine:3.20"},
		{name: "registry port", dockerfile: "FROM localhost:5000/team/app:dev", wantBases: []string{"localhost:5000/team/app:dev"}, wantExternal: []string{"localhost:5000/team/app:dev"}, wantFinalExternal: "localhost:5000/team/app:dev"},
		{name: "quoted from ref", dockerfile: "FROM 'alpine:3.20'", wantBases: []string{"alpine:3.20"}, wantExternal: []string{"alpine:3.20"}, wantFinalExternal: "alpine:3.20"},
		{name: "escaped whitespace in quoted arg", dockerfile: "ARG BASE='busybox:1.36'\nFROM ${BASE}", wantBases: []string{"busybox:1.36"}, wantExternal: []string{"busybox:1.36"}, wantFinalExternal: "busybox:1.36"},
		{name: "nested arg defaults", dockerfile: "ARG DIST=alpine\nARG BASE=${DIST}:3.20\nFROM ${BASE}", wantBases: []string{"alpine:3.20"}, wantExternal: []string{"alpine:3.20"}, wantFinalExternal: "alpine:3.20"},
		{name: "duplicate unresolved unique", dockerfile: "FROM ${BASE}-${BASE}", wantBases: []string{"${BASE}-${BASE}"}, wantExternal: []string{"${BASE}-${BASE}"}, wantFinalExternal: "${BASE}-${BASE}", wantUnresolved: []string{"BASE"}},
		{name: "continuation comment", dockerfile: "FROM \\\n  alpine:3.20 \\\n  # comment\n", wantBases: []string{"alpine:3.20"}, wantExternal: []string{"alpine:3.20"}, wantFinalExternal: "alpine:3.20"},
	}
	if len(cases) < 30 {
		t.Fatalf("parser fixture count = %d, want at least 30", len(cases))
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ParseDockerfile(tt.dockerfile, ParseOptions{BuildArgs: tt.args, Target: tt.target})
			gotBases := make([]string, 0, len(got.Stages))
			gotExternal := []string{}
			for _, stage := range got.Stages {
				gotBases = append(gotBases, stage.BaseResolved)
				if !stage.Internal && !stage.Scratch {
					gotExternal = append(gotExternal, stage.BaseResolved)
				}
			}
			if !sameStrings(gotBases, tt.wantBases) {
				t.Fatalf("bases = %v, want %v", gotBases, tt.wantBases)
			}
			if !sameStrings(gotExternal, tt.wantExternal) {
				t.Fatalf("external bases = %v, want %v", gotExternal, tt.wantExternal)
			}
			finalExternal := ""
			if index := got.FinalExternalStageIndex(); index >= 0 {
				finalExternal = got.Stages[index].BaseResolved
			}
			if finalExternal != tt.wantFinalExternal {
				t.Fatalf("final external = %q, want %q", finalExternal, tt.wantFinalExternal)
			}
			if !sameStrings(got.UnresolvedArgs, tt.wantUnresolved) {
				t.Fatalf("unresolved = %v, want %v", got.UnresolvedArgs, tt.wantUnresolved)
			}
			if len(got.Warnings) != tt.wantWarnings {
				t.Fatalf("warnings = %v, want count %d", got.Warnings, tt.wantWarnings)
			}
			if tt.wantPlatform != "" && got.Stages[0].Platform != tt.wantPlatform {
				t.Fatalf("platform = %q, want %q", got.Stages[0].Platform, tt.wantPlatform)
			}
			if tt.wantPinned && !got.Stages[0].Pinned {
				t.Fatalf("first stage not marked pinned")
			}
			if tt.wantScratch && !got.Stages[0].Scratch {
				t.Fatalf("first stage not marked scratch")
			}
			if tt.wantInternal && !got.Stages[len(got.Stages)-1].Internal {
				t.Fatalf("last stage not marked internal")
			}
		})
	}
}

func TestBaseRefsFromParseStatuses(t *testing.T) {
	t.Parallel()
	parsed := ParseDockerfile(`
ARG BASE
FROM ${BASE}:latest AS unresolved
FROM alpine@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa AS pinned
FROM nginx:alpine AS runtime
`, ParseOptions{Target: "runtime"})
	refs := baseRefsFromParse(parsed)
	if len(refs) != 3 {
		t.Fatalf("refs = %#v", refs)
	}
	if refs[0].Status != models.UpdateStatusUnknownBaseImage {
		t.Fatalf("unresolved status = %s", refs[0].Status)
	}
	if refs[1].Status != models.UpdateStatusPinnedDigest {
		t.Fatalf("pinned status = %s", refs[1].Status)
	}
	if !refs[2].IsFinalStageBase || refs[2].Name != "nginx" || refs[2].Tag != "alpine" {
		t.Fatalf("runtime ref = %#v", refs[2])
	}
}

func sameStrings(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
