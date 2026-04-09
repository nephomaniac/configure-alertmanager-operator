package pko

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"
)

// deployPkoDir returns the path to the deploy_pko directory relative to the repo root.
func deployPkoDir() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "deploy_pko")); err == nil {
			return filepath.Join(dir, "deploy_pko")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "deploy_pko"
}

// templateFuncMap provides the functions available in PKO gotmpls.
// CAMO currently doesn't use any custom functions, but we include
// common ones in case they're added in the future.
func templateFuncMap() template.FuncMap {
	return template.FuncMap{
		"quote": func(v interface{}) string {
			return fmt.Sprintf("%q", v)
		},
	}
}

// renderTemplate renders a gotmpl file with the given config data and returns the output.
func renderTemplate(t *testing.T, filename string, data map[string]interface{}) string {
	t.Helper()
	tmplPath := filepath.Join(deployPkoDir(), filename)
	content, err := os.ReadFile(tmplPath)
	if err != nil {
		t.Fatalf("failed to read template %s: %v", tmplPath, err)
	}

	tmpl, err := template.New(filename).Funcs(templateFuncMap()).Parse(string(content))
	if err != nil {
		t.Fatalf("failed to parse template %s: %v", filename, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("failed to execute template %s: %v", filename, err)
	}
	return buf.String()
}

// parseYAMLDocument parses a single YAML document into a map.
func parseYAMLDocument(t *testing.T, data string) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	if err := yaml.Unmarshal([]byte(data), &result); err != nil {
		t.Fatalf("failed to parse YAML: %v\nContent:\n%s", err, data)
	}
	return result
}

// parseYAMLMultiDoc parses a multi-document YAML file (separated by ---).
func parseYAMLMultiDoc(t *testing.T, data string) []map[string]interface{} {
	t.Helper()
	var results []map[string]interface{}
	decoder := yaml.NewDecoder(strings.NewReader(data))
	for {
		var doc map[string]interface{}
		if err := decoder.Decode(&doc); err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("failed to parse YAML document: %v", err)
		}
		if doc != nil {
			results = append(results, doc)
		}
	}
	return results
}

// defaultConfig returns a production-like config with operator image.
func defaultConfig() map[string]interface{} {
	return map[string]interface{}{
		"config": map[string]interface{}{
			"image": "quay.io/example/configure-alertmanager-operator:v1.0.0",
		},
	}
}

// =============================================================================
// Deployment Template Tests
// =============================================================================

func TestDeploymentGotmpl(t *testing.T) {
	output := renderTemplate(t, "Deployment-configure-alertmanager-operator.yaml.gotmpl", defaultConfig())
	doc := parseYAMLDocument(t, output)

	// Verify basic resource metadata
	if doc["kind"] != "Deployment" {
		t.Errorf("expected kind=Deployment, got %v", doc["kind"])
	}

	metadata := doc["metadata"].(map[string]interface{})
	if metadata["name"] != "configure-alertmanager-operator" {
		t.Errorf("expected name=configure-alertmanager-operator, got %v", metadata["name"])
	}
	if metadata["namespace"] != "openshift-monitoring" {
		t.Errorf("expected namespace=openshift-monitoring, got %v", metadata["namespace"])
	}

	// Verify PKO phase annotation
	annotations := metadata["annotations"].(map[string]interface{})
	if annotations["package-operator.run/phase"] != "deploy" {
		t.Errorf("expected phase=deploy, got %v", annotations["package-operator.run/phase"])
	}
	if annotations["package-operator.run/collision-protection"] != "IfNoController" {
		t.Errorf("expected collision-protection=IfNoController, got %v", annotations["package-operator.run/collision-protection"])
	}

	// Verify config.image substitution
	if !strings.Contains(output, "quay.io/example/configure-alertmanager-operator:v1.0.0") {
		t.Error("expected config.image to be substituted in Deployment")
	}

	spec := doc["spec"].(map[string]interface{})

	// Verify replica count
	replicas, ok := spec["replicas"].(int)
	if !ok || replicas != 1 {
		t.Errorf("expected replicas=1, got %v", spec["replicas"])
	}

	// Verify selector matches template labels
	selector := spec["selector"].(map[string]interface{})
	matchLabels := selector["matchLabels"].(map[string]interface{})
	if matchLabels["name"] != "configure-alertmanager-operator" {
		t.Errorf("expected selector label name=configure-alertmanager-operator, got %v", matchLabels["name"])
	}

	template := spec["template"].(map[string]interface{})
	templateMeta := template["metadata"].(map[string]interface{})
	templateLabels := templateMeta["labels"].(map[string]interface{})
	if templateLabels["name"] != "configure-alertmanager-operator" {
		t.Errorf("expected template label name=configure-alertmanager-operator, got %v", templateLabels["name"])
	}

	// Verify ServiceAccount
	templateSpec := template["spec"].(map[string]interface{})
	if templateSpec["serviceAccountName"] != "configure-alertmanager-operator" {
		t.Errorf("expected serviceAccountName=configure-alertmanager-operator, got %v", templateSpec["serviceAccountName"])
	}

	// Verify container configuration
	containers := templateSpec["containers"].([]interface{})
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}
	container := containers[0].(map[string]interface{})
	if container["name"] != "configure-alertmanager-operator" {
		t.Errorf("expected container name=configure-alertmanager-operator, got %v", container["name"])
	}

	// Verify WATCH_NAMESPACE env var for namespace scoping
	env := container["env"].([]interface{})
	hasWatchNamespace := false
	for _, e := range env {
		envVar := e.(map[string]interface{})
		if envVar["name"] == "WATCH_NAMESPACE" {
			hasWatchNamespace = true
			valueFrom := envVar["valueFrom"].(map[string]interface{})
			fieldRef := valueFrom["fieldRef"].(map[string]interface{})
			if fieldRef["fieldPath"] != "metadata.namespace" {
				t.Errorf("WATCH_NAMESPACE should use fieldRef to metadata.namespace")
			}
		}
	}
	if !hasWatchNamespace {
		t.Error("expected WATCH_NAMESPACE env var for namespace scoping")
	}
}

// =============================================================================
// ServiceAccount Template Tests
// =============================================================================

func TestServiceAccountGotmpl(t *testing.T) {
	output := renderTemplate(t, "ServiceAccount-configure-alertmanager-operator.yaml.gotmpl", defaultConfig())
	doc := parseYAMLDocument(t, output)

	if doc["kind"] != "ServiceAccount" {
		t.Errorf("expected kind=ServiceAccount, got %v", doc["kind"])
	}

	metadata := doc["metadata"].(map[string]interface{})
	if metadata["name"] != "configure-alertmanager-operator" {
		t.Errorf("expected name=configure-alertmanager-operator, got %v", metadata["name"])
	}
	if metadata["namespace"] != "openshift-monitoring" {
		t.Errorf("expected namespace=openshift-monitoring, got %v", metadata["namespace"])
	}

	// Verify PKO phase annotation
	annotations := metadata["annotations"].(map[string]interface{})
	if annotations["package-operator.run/phase"] != "rbac" {
		t.Errorf("expected phase=rbac, got %v", annotations["package-operator.run/phase"])
	}
}

// =============================================================================
// Role Template Tests
// =============================================================================

func TestRoleGotmpl(t *testing.T) {
	output := renderTemplate(t, "Role-configure-alertmanager-operator.yaml.gotmpl", defaultConfig())
	doc := parseYAMLDocument(t, output)

	if doc["kind"] != "Role" {
		t.Errorf("expected kind=Role, got %v", doc["kind"])
	}

	metadata := doc["metadata"].(map[string]interface{})
	if metadata["name"] != "configure-alertmanager-operator" {
		t.Errorf("expected name=configure-alertmanager-operator, got %v", metadata["name"])
	}
	if metadata["namespace"] != "openshift-monitoring" {
		t.Errorf("expected namespace=openshift-monitoring, got %v", metadata["namespace"])
	}

	// Verify PKO phase annotation
	annotations := metadata["annotations"].(map[string]interface{})
	if annotations["package-operator.run/phase"] != "rbac" {
		t.Errorf("expected phase=rbac, got %v", annotations["package-operator.run/phase"])
	}

	// Verify rules exist (operator needs permissions)
	rules, ok := doc["rules"].([]interface{})
	if !ok || len(rules) == 0 {
		t.Error("Role should have at least one rule")
	}
}

// =============================================================================
// RoleBinding Template Tests
// =============================================================================

func TestRoleBindingGotmpl(t *testing.T) {
	output := renderTemplate(t, "RoleBinding-configure-alertmanager-operator.yaml.gotmpl", defaultConfig())
	doc := parseYAMLDocument(t, output)

	if doc["kind"] != "RoleBinding" {
		t.Errorf("expected kind=RoleBinding, got %v", doc["kind"])
	}

	metadata := doc["metadata"].(map[string]interface{})
	if metadata["name"] != "configure-alertmanager-operator" {
		t.Errorf("expected name=configure-alertmanager-operator, got %v", metadata["name"])
	}

	// Verify roleRef references the correct Role
	roleRef := doc["roleRef"].(map[string]interface{})
	if roleRef["kind"] != "Role" {
		t.Errorf("expected roleRef.kind=Role, got %v", roleRef["kind"])
	}
	if roleRef["name"] != "configure-alertmanager-operator" {
		t.Errorf("expected roleRef.name=configure-alertmanager-operator, got %v", roleRef["name"])
	}

	// Verify subject references the correct ServiceAccount
	subjects := doc["subjects"].([]interface{})
	if len(subjects) != 1 {
		t.Fatalf("expected 1 subject, got %d", len(subjects))
	}
	subject := subjects[0].(map[string]interface{})
	if subject["kind"] != "ServiceAccount" {
		t.Errorf("expected subject.kind=ServiceAccount, got %v", subject["kind"])
	}
	if subject["name"] != "configure-alertmanager-operator" {
		t.Errorf("expected subject.name=configure-alertmanager-operator, got %v", subject["name"])
	}
	// Note: For RoleBindings in the same namespace as the subject,
	// the namespace field is optional and may be omitted
}

// =============================================================================
// ClusterRole Template Tests (edit/view)
// =============================================================================

func TestClusterRoleEditGotmpl(t *testing.T) {
	output := renderTemplate(t, "ClusterRole-configure-alertmanager-operator-edit.yaml.gotmpl", defaultConfig())
	doc := parseYAMLDocument(t, output)

	if doc["kind"] != "ClusterRole" {
		t.Errorf("expected kind=ClusterRole, got %v", doc["kind"])
	}

	metadata := doc["metadata"].(map[string]interface{})
	if !strings.Contains(fmt.Sprintf("%v", metadata["name"]), "edit") {
		t.Errorf("expected name to contain 'edit', got %v", metadata["name"])
	}

	// Verify PKO phase annotation
	annotations := metadata["annotations"].(map[string]interface{})
	if annotations["package-operator.run/phase"] != "rbac" {
		t.Errorf("expected phase=rbac, got %v", annotations["package-operator.run/phase"])
	}
}

func TestClusterRoleViewGotmpl(t *testing.T) {
	output := renderTemplate(t, "ClusterRole-configure-alertmanager-operator-view.yaml.gotmpl", defaultConfig())
	doc := parseYAMLDocument(t, output)

	if doc["kind"] != "ClusterRole" {
		t.Errorf("expected kind=ClusterRole, got %v", doc["kind"])
	}

	metadata := doc["metadata"].(map[string]interface{})
	if !strings.Contains(fmt.Sprintf("%v", metadata["name"]), "view") {
		t.Errorf("expected name to contain 'view', got %v", metadata["name"])
	}
}

// =============================================================================
// Cleanup-OLM-Job Tests (CRITICAL for migration)
// =============================================================================

func TestCleanupOLMJob(t *testing.T) {
	// Read the Cleanup-OLM-Job.yaml file (not a template)
	jobPath := filepath.Join(deployPkoDir(), "Cleanup-OLM-Job.yaml")
	content, err := os.ReadFile(jobPath)
	if err != nil {
		t.Fatalf("failed to read Cleanup-OLM-Job.yaml: %v", err)
	}

	// Parse all documents in the file (ServiceAccount, Role, RoleBinding, Job)
	docs := parseYAMLMultiDoc(t, string(content))

	if len(docs) != 4 {
		t.Fatalf("expected 4 resources in Cleanup-OLM-Job.yaml (ServiceAccount, Role, RoleBinding, Job), got %d", len(docs))
	}

	// Track what we've found
	var (
		serviceAccount *map[string]interface{}
		role           *map[string]interface{}
		roleBinding    *map[string]interface{}
		job            *map[string]interface{}
	)

	for i := range docs {
		doc := &docs[i]
		kind := (*doc)["kind"]
		switch kind {
		case "ServiceAccount":
			serviceAccount = doc
		case "Role":
			role = doc
		case "RoleBinding":
			roleBinding = doc
		case "Job":
			job = doc
		}
	}

	// Verify all resources were found
	if serviceAccount == nil {
		t.Fatal("ServiceAccount not found in Cleanup-OLM-Job.yaml")
	}
	if role == nil {
		t.Fatal("Role not found in Cleanup-OLM-Job.yaml")
	}
	if roleBinding == nil {
		t.Fatal("RoleBinding not found in Cleanup-OLM-Job.yaml")
	}
	if job == nil {
		t.Fatal("Job not found in Cleanup-OLM-Job.yaml")
	}

	// Test ServiceAccount
	t.Run("ServiceAccount", func(t *testing.T) {
		metadata := (*serviceAccount)["metadata"].(map[string]interface{})
		if metadata["name"] != "olm-cleanup" {
			t.Errorf("expected ServiceAccount name=olm-cleanup, got %v", metadata["name"])
		}
		if metadata["namespace"] != "openshift-monitoring" {
			t.Errorf("expected ServiceAccount namespace=openshift-monitoring, got %v", metadata["namespace"])
		}

		annotations := metadata["annotations"].(map[string]interface{})
		if annotations["package-operator.run/phase"] != "cleanup-rbac" {
			t.Errorf("expected ServiceAccount phase=cleanup-rbac, got %v", annotations["package-operator.run/phase"])
		}
	})

	// Test Role
	t.Run("Role", func(t *testing.T) {
		metadata := (*role)["metadata"].(map[string]interface{})
		if metadata["name"] != "olm-cleanup" {
			t.Errorf("expected Role name=olm-cleanup, got %v", metadata["name"])
		}

		// Verify permissions include CSV and Subscription deletion
		rules := (*role)["rules"].([]interface{})
		if len(rules) == 0 {
			t.Fatal("Role should have at least one rule")
		}

		rule := rules[0].(map[string]interface{})

		// Verify API groups
		apiGroups := rule["apiGroups"].([]interface{})
		hasOperatorsAPI := false
		for _, group := range apiGroups {
			if group == "operators.coreos.com" {
				hasOperatorsAPI = true
			}
		}
		if !hasOperatorsAPI {
			t.Error("Role should have operators.coreos.com API group for OLM cleanup")
		}

		// Verify resources
		resources := rule["resources"].([]interface{})
		hasCSV := false
		hasSubscription := false
		for _, res := range resources {
			if res == "clusterserviceversions" {
				hasCSV = true
			}
			if res == "subscriptions" {
				hasSubscription = true
			}
		}
		if !hasCSV {
			t.Error("Role should have clusterserviceversions resource for CSV deletion")
		}
		if !hasSubscription {
			t.Error("Role should have subscriptions resource for Subscription deletion")
		}

		// Verify delete verb
		verbs := rule["verbs"].([]interface{})
		hasDelete := false
		for _, verb := range verbs {
			if verb == "delete" {
				hasDelete = true
			}
		}
		if !hasDelete {
			t.Error("Role should have 'delete' verb to clean up OLM resources")
		}
	})

	// Test Job (the actual cleanup logic)
	t.Run("Job", func(t *testing.T) {
		metadata := (*job)["metadata"].(map[string]interface{})
		if metadata["name"] != "olm-cleanup" {
			t.Errorf("expected Job name=olm-cleanup, got %v", metadata["name"])
		}
		if metadata["namespace"] != "openshift-monitoring" {
			t.Errorf("expected Job namespace=openshift-monitoring, got %v", metadata["namespace"])
		}

		annotations := metadata["annotations"].(map[string]interface{})

		// CRITICAL: Job must run in cleanup-deploy phase (BEFORE deploy phase)
		if annotations["package-operator.run/phase"] != "cleanup-deploy" {
			t.Errorf("CRITICAL: Job must run in cleanup-deploy phase (before deploy), got %v", annotations["package-operator.run/phase"])
		}

		spec := (*job)["spec"].(map[string]interface{})
		tmpl := spec["template"].(map[string]interface{})
		templateSpec := tmpl["spec"].(map[string]interface{})

		// Verify ServiceAccount
		if templateSpec["serviceAccountName"] != "olm-cleanup" {
			t.Errorf("expected Job serviceAccountName=olm-cleanup, got %v", templateSpec["serviceAccountName"])
		}

		// Verify containers
		containers := templateSpec["containers"].([]interface{})
		if len(containers) != 1 {
			t.Fatalf("expected 1 container, got %d", len(containers))
		}
		container := containers[0].(map[string]interface{})

		// Verify command deletes correct CSV
		command := container["command"].([]interface{})
		if len(command) < 3 {
			t.Fatal("expected command with shell script")
		}

		// The script is in the 3rd element (index 2): sh -c "script here"
		script := fmt.Sprintf("%v", command[2])

		// CRITICAL: Verify CSV deletion uses correct label selector
		expectedLabelSelector := "operators.coreos.com/configure-alertmanager-operator.openshift-monitoring"
		if !strings.Contains(script, expectedLabelSelector) {
			t.Errorf("CRITICAL: Job should delete CSV with label selector '%s', but script doesn't contain it:\n%s",
				expectedLabelSelector, script)
		}

		// Verify script deletes CSV
		if !strings.Contains(script, "delete csv") {
			t.Error("CRITICAL: Job script should delete CSV (ClusterServiceVersion)")
		}

		// Verify script uses -n openshift-monitoring
		if !strings.Contains(script, "-n openshift-monitoring") {
			t.Error("CRITICAL: Job script should specify namespace (-n openshift-monitoring)")
		}
	})
}

// =============================================================================
// All Templates Render Successfully
// =============================================================================

func TestAllGotmplsRenderWithDefaults(t *testing.T) {
	dir := deployPkoDir()
	matches, err := filepath.Glob(filepath.Join(dir, "*.gotmpl"))
	if err != nil {
		t.Fatalf("failed to glob gotmpls: %v", err)
	}

	if len(matches) == 0 {
		t.Fatal("no .gotmpl files found in deploy_pko/")
	}

	config := defaultConfig()
	for _, match := range matches {
		filename := filepath.Base(match)
		t.Run(filename, func(t *testing.T) {
			output := renderTemplate(t, filename, config)

			// Verify output is valid YAML
			parseYAMLDocument(t, output)

			// Verify all templates have PKO phase annotation
			if !strings.Contains(output, "package-operator.run/phase") {
				t.Errorf("%s should have package-operator.run/phase annotation", filename)
			}
		})
	}
}
