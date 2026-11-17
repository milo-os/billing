// SPDX-License-Identifier: AGPL-3.0-only

package activitypolicy_test

import (
	"os"
	"path/filepath"
	"testing"

	"sigs.k8s.io/yaml"
)

// The owner the activity processor reads from a policy's labels to route the
// alerts for a failing policy. A policy without them alerts as the platform's,
// not billing's. The platform's service catalog lists this repository under
// the billing service, team billing.
var policyOwner = map[string]string{
	"meta.datumapis.com/service": "billing",
	"meta.datumapis.com/team":    "billing",
}

type policiesComponent struct {
	Resources []string `json:"resources"`
	Labels    []struct {
		Pairs map[string]string `json:"pairs"`
	} `json:"labels"`
}

func readPoliciesComponent(t *testing.T) policiesComponent {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(filepath.Dir(policiesGlob), "kustomization.yaml"))
	if err != nil {
		t.Fatalf("read the policies' kustomization: %v", err)
	}
	var c policiesComponent
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatalf("parse the policies' kustomization: %v", err)
	}
	return c
}

// declaredOwner returns the labels the component sets on its policies, by
// label name.
func declaredOwner(c policiesComponent) map[string]string {
	owner := map[string]string{}
	for _, l := range c.Labels {
		for k, v := range l.Pairs {
			owner[k] = v
		}
	}
	return owner
}

// unlistedPolicyFiles returns the policy files in the folder that the
// component does not list in its resources.
func unlistedPolicyFiles(t *testing.T, c policiesComponent) []string {
	t.Helper()
	files, err := filepath.Glob(policiesGlob)
	if err != nil {
		t.Fatalf("glob %q: %v", policiesGlob, err)
	}
	if len(files) == 0 {
		t.Fatalf("no policy files matched %q", policiesGlob)
	}
	listed := map[string]bool{}
	for _, r := range c.Resources {
		listed[r] = true
	}
	var unlisted []string
	for _, f := range files {
		if name := filepath.Base(f); !listed[name] {
			unlisted = append(unlisted, name)
		}
	}
	return unlisted
}

// TestEveryPolicyDeclaresItsOwner fails when the policies' component sets no
// owner or a wrong one, or when a policy file sits outside its resources,
// where the component's owner labels would not reach it.
func TestEveryPolicyDeclaresItsOwner(t *testing.T) {
	c := readPoliciesComponent(t)
	owner := declaredOwner(c)
	for label, want := range policyOwner {
		if got := owner[label]; got != want {
			t.Errorf("the policies' kustomization sets %s to %q on its policies, want %q", label, got, want)
		}
	}
	for _, name := range unlistedPolicyFiles(t, c) {
		t.Errorf("%s: not in the policies' resources, so the owner labels never reach it", name)
	}
}
