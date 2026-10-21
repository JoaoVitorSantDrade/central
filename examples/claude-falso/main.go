// claude-falso imita o Claude Code CLI nos testes do executor de vagas: grava
// os argumentos e o stdin que recebeu e responde um stream-json gravado.
//
// Variáveis de ambiente:
//   - CLAUDE_FALSO_ARGS: arquivo onde grava os argumentos (um por linha) e,
//     depois de uma linha "--stdin--", o texto lido do stdin;
//   - CLAUDE_FALSO_DORMIR: duração (ex.: 30s) que demora antes de responder;
//   - CLAUDE_FALSO_SAIR: código de saída;
//   - CLAUDE_FALSO_LIMITE: responde o erro de limite de uso; um número é o epoch do reset;
//   - CLAUDE_FALSO_NEGAR: põe um item em permission_denials com esse texto.
//
// Com "auth status" sai com 0 (ou CLAUDE_FALSO_SAIR); com -w <nome> cria
// <cwd>/.claude/worktrees/<nome> e informa esse cwd no init.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	args := os.Args[1:]
	entrada := ""
	if len(args) > 0 && args[0] == "-p" {
		b, _ := io.ReadAll(os.Stdin)
		entrada = string(b)
	}
	if f := os.Getenv("CLAUDE_FALSO_ARGS"); f != "" {
		txt := strings.Join(args, "\n") + "\n"
		if entrada != "" {
			txt += "--stdin--\n" + entrada + "\n"
		}
		if err := os.WriteFile(f, []byte(txt), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	codigo := 0
	if v := os.Getenv("CLAUDE_FALSO_SAIR"); v != "" {
		codigo, _ = strconv.Atoi(v)
	}
	if len(args) == 0 || args[0] != "-p" {
		// auth status, --desktop etc.: só os argumentos e o código.
		os.Exit(codigo)
	}

	sessao := ""
	cwd, _ := os.Getwd()
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--session-id", "--resume":
			sessao = args[i+1]
		case "-w":
			cwd = filepath.Join(cwd, ".claude", "worktrees", args[i+1])
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
		}
	}
	emit := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Println(string(b))
	}
	fmt.Println("linha que não é JSON")
	emit(map[string]any{"type": "system", "subtype": "init", "session_id": sessao, "cwd": cwd})
	if d, err := time.ParseDuration(os.Getenv("CLAUDE_FALSO_DORMIR")); err == nil {
		time.Sleep(d)
	}
	if v, ok := os.LookupEnv("CLAUDE_FALSO_LIMITE"); ok {
		txt := "Claude AI usage limit reached"
		if v != "" {
			txt += "|" + v
		}
		emit(map[string]any{"type": "result", "subtype": "success", "is_error": true, "result": txt, "session_id": sessao})
		os.Exit(1)
	}
	negacoes := []any{}
	if v := os.Getenv("CLAUDE_FALSO_NEGAR"); v != "" {
		negacoes = append(negacoes, map[string]any{"tool_name": "Bash", "tool_use_id": "t1", "tool_input": map[string]any{"command": v}})
	}
	if codigo != 0 {
		fmt.Fprintln(os.Stderr, "falha simulada")
		emit(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "result": "falhou", "session_id": sessao})
		os.Exit(codigo)
	}
	emit(map[string]any{
		"type": "result", "subtype": "success", "is_error": false, "result": "ok", "session_id": sessao,
		"total_cost_usd": 0.01, "num_turns": 1,
		"usage":              map[string]any{"input_tokens": 10, "output_tokens": 5},
		"permission_denials": negacoes,
	})
}
