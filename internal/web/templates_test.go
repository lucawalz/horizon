package web

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPagesRenderEveryState(t *testing.T) {
	pages, err := parsePages()
	if err != nil {
		t.Fatalf("parse the pages: %v", err)
	}
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	populated := newLeaseDetail(leaseFixture("render-run"), now)
	populated.Conditions = newConditionRows(activeStatus(now).Conditions, now)
	populated.Instances = newInstanceRows(activeStatus(now).Instances, now)

	for name, testCase := range map[string]struct {
		page  page
		block string
		data  any
	}{
		"empty lease list":     {leasesPage, layoutTemplate, newView(leaseListTitle, leaseTable{})},
		"filled lease list":    {leasesPage, layoutTemplate, newView(leaseListTitle, newLeaseTable(nil, now))},
		"lease table fragment": {leasesPage, leaseTableTemplate, leaseTable{Rows: []leaseRow{newLeaseRow(leaseFixture("render-run"), now)}}},
		"bare lease detail":    {leasePage, layoutTemplate, newView("lease", leaseDetail{})},
		"filled lease detail":  {leasePage, layoutTemplate, newView("lease", populated)},
		"lease body fragment":  {leasePage, leaseBodyTemplate, populated},
		"empty machines":       {machinesPage, layoutTemplate, newView(machineTitle, machineView{Notice: chooseNotice})},
		"filled machines": {machinesPage, layoutTemplate, newView(machineTitle, machineView{
			Configs: []machineConfigRow{{Name: "hetzner", Type: "hetzner", Ready: "True", Age: "1h"}},
			Config:  "hetzner",
			Region:  "nbg1",
			Types:   []machineTypeRow{newMachineTypeRow(offeredType("cx22", "nbg1"))},
		})},
		"error": {errorPage, layoutTemplate, newView("Not Found", failure{Heading: "Not Found", Detail: "no capacity lease named \"absent\" exists in the cluster"})},
	} {
		t.Run(name, func(t *testing.T) {
			var body bytes.Buffer
			if err := pages[testCase.page].ExecuteTemplate(&body, testCase.block, testCase.data); err != nil {
				t.Fatalf("render: %v", err)
			}
			if body.Len() == 0 {
				t.Error("the template rendered nothing")
			}
		})
	}
}

func TestPagesPollTheirOwnFragment(t *testing.T) {
	testEnv.SkipUnlessRunning(t)

	createLease(t, leaseFixture("poll-run"), activeStatus(time.Now()))
	server := newTestServer(t, testEnv.Client, AbsentCatalogue())

	for target, want := range map[string]string{
		"/":                `hx-get="/fragments/leases" hx-trigger="every 5s"`,
		"/leases/poll-run": `hx-get="/fragments/leases/poll-run" hx-trigger="every 5s"`,
	} {
		response := get(t, server, target)
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("%s does not poll, want %q", target, want)
		}
	}
}

func TestEmbeddedAssetsAreServed(t *testing.T) {
	server := newTestServer(t, failingReader{err: errors.New("unused")}, AbsentCatalogue())

	for _, asset := range []string{"/assets/htmx.min.js", "/assets/style.css"} {
		response := get(t, server, asset)
		if response.Code != http.StatusOK {
			t.Errorf("%s status = %d, want %d", asset, response.Code, http.StatusOK)
		}
		if response.Body.Len() == 0 {
			t.Errorf("%s is empty", asset)
		}
	}
}
