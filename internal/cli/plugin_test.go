package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pluginDir = "../../integrations/claude-code"

func lerJSON(t *testing.T, p string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s não é JSON: %v", p, err)
	}
	return m
}

// comandoDoHook devolve os argumentos (sem o "central") do hook SessionStart.
func comandoDoHook(t *testing.T) []string {
	t.Helper()
	h := lerJSON(t, filepath.Join(pluginDir, "hooks", "hooks.json"))
	ss := h["hooks"].(map[string]any)["SessionStart"].([]any)[0].(map[string]any)
	cmd := ss["hooks"].([]any)[0].(map[string]any)["command"].(string)
	campos := strings.Fields(cmd)
	if campos[0] != "central" {
		t.Fatalf("hook chama %q", campos[0])
	}
	return campos[1:]
}

func TestPluginManifestosValidos(t *testing.T) {
	if m := lerJSON(t, filepath.Join(pluginDir, ".claude-plugin", "plugin.json")); m["name"] != "central" {
		t.Fatalf("plugin.json = %v", m)
	}
	mcp := lerJSON(t, filepath.Join(pluginDir, ".mcp.json"))["mcpServers"].(map[string]any)["central"].(map[string]any)
	if mcp["command"] != "central" || mcp["args"].([]any)[0] != "mcp" {
		t.Fatalf(".mcp.json = %v", mcp)
	}
	mk := lerJSON(t, filepath.Join("..", "..", ".claude-plugin", "marketplace.json"))
	src := mk["plugins"].([]any)[0].(map[string]any)["source"].(string)
	if _, err := os.Stat(filepath.Join("..", "..", src, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatalf("marketplace aponta para %s: %v", src, err)
	}
	skill, err := os.ReadFile(filepath.Join(pluginDir, "skills", "central", "SKILL.md"))
	if err != nil || !strings.HasPrefix(string(skill), "---\nname: central\n") || !strings.Contains(string(skill), "central esperar") {
		t.Fatalf("SKILL.md inválida: %v", err)
	}
}

func TestBriefSeProjetoForaDeProjetoSilencioso(t *testing.T) {
	e := newEnv(t)
	args := comandoDoHook(t)
	for _, dir := range []string{t.TempDir(), e.project("sem-entrar")} {
		out, err := e.cmd(dir, "", args...).Output()
		if err != nil || len(strings.TrimSpace(string(out))) != 0 {
			t.Fatalf("hook em %s: %q, %v", dir, out, err)
		}
	}
	d := e.project("com-vaga")
	e.ok(d, "entrar", "--vaga", "F1", "--papel", "EXECUTOR")
	out, err := e.cmd(d, "", args...).Output()
	if err != nil || !strings.Contains(string(out), "## 1. Quem você é") || strings.Contains(string(out), "## 4.") {
		t.Fatalf("hook com vaga: %q, %v", out, err)
	}
}
