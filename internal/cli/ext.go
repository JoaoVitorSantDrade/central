package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/JoaoVitorSantDrade/central/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// configLocal lê o central.toml do projeto da pasta atual.
func configLocal(proj config.Project) (config.ProjectConfig, error) {
	data, err := os.ReadFile(filepath.Join(proj.Root, config.FileName))
	if err != nil {
		return config.ProjectConfig{}, err
	}
	return config.ParseProjectConfig(data)
}

// runExtensao trata "central <extensao> <acao> ..." quando o comando é uma
// extensão do central.toml local; ok=false quando não é.
func runExtensao(cmd string, rest []string, stdin io.Reader) (map[string]any, bool, error) {
	proj, c, base, err := projectClient()
	if err != nil {
		return nil, false, nil
	}
	cfg, err := configLocal(proj)
	if err != nil {
		return nil, false, nil
	}
	e, ok := cfg.Extensao(cmd)
	if !ok {
		return nil, false, nil
	}
	if len(rest) == 0 || !slices.Contains(e.CLI, rest[0]) {
		return nil, true, usage("%s precisa de uma ação: %s", cmd, strings.Join(e.CLI, ", "))
	}
	args, err := argsExtensao(rest[1:], stdin)
	if err != nil {
		return nil, true, err
	}
	m, err := chamarExtensao(c, base, proj.Root, cmd, rest[0], args)
	return m, true, err
}

// argsExtensao converte --k v, --k=v e posicionais nos args da extensão:
// flags viram texto, posicionais vão em "_" e "-" lê o stdin para "stdin".
func argsExtensao(rest []string, stdin io.Reader) (map[string]any, error) {
	args := map[string]any{}
	var pos []string
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "-":
			b, err := io.ReadAll(stdin)
			if err != nil {
				return nil, err
			}
			args["stdin"] = string(b)
		case strings.HasPrefix(a, "--") && len(a) > 2:
			k, v, temValor := strings.Cut(a[2:], "=")
			if !temValor {
				if i+1 >= len(rest) {
					return nil, usage("--%s precisa de um valor", k)
				}
				i++
				v = rest[i]
			}
			args[k] = v
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) > 0 {
		args["_"] = pos
	}
	return args, nil
}

func chamarExtensao(c *client, base, pasta, nome, acao string, args map[string]any) (map[string]any, error) {
	return hintEntrar(callWithDaemon(c, "POST", base+"/extensoes/"+url.PathEscape(nome)+"/"+url.PathEscape(acao),
		map[string]any{"pasta": pasta, "args": args}))
}

// ferramentaExtensao é a ferramenta MCP que chama qualquer ação de CLI de uma
// extensão do projeto, com args como objeto.
func ferramentaExtensao(srv *mcp.Server) {
	schema := map[string]any{"type": "object", "required": []string{"nome", "acao"}, "properties": map[string]any{
		"nome": map[string]any{"type": "string", "description": "extensão (do central.toml)"},
		"acao": map[string]any{"type": "string", "description": "ação de CLI da extensão"},
		"args": map[string]any{"type": "object", "description": "argumentos da ação"},
	}}
	srv.AddTool(&mcp.Tool{Name: "extensao", Description: "Chama uma ação de uma extensão do projeto (ext/v1).", InputSchema: schema},
		func(ctx context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var in struct {
				Nome, Acao string
				Args       map[string]any
			}
			if err := json.Unmarshal(r.Params.Arguments, &in); err != nil || in.Nome == "" || in.Acao == "" {
				return resultadoMCP(nil, usage("extensao precisa de nome e acao")), nil
			}
			m, err := extensaoPorNome(in.Nome, in.Acao, in.Args)
			return resultadoMCP(m, err), nil
		})
}

// extensaoPorNome chama a ação de CLI da extensão do projeto da pasta atual.
func extensaoPorNome(nome, acao string, args map[string]any) (map[string]any, error) {
	proj, c, base, err := projectClient()
	if err != nil {
		return nil, err
	}
	cfg, err := configLocal(proj)
	if err != nil {
		return nil, newErr(ExitRecusado, "invalido", "%v", err)
	}
	e, ok := cfg.Extensao(nome)
	if !ok {
		return nil, newErr(ExitNaoAchado, "nao_encontrado", "extensão %q não está no central.toml", nome)
	}
	if !slices.Contains(e.CLI, acao) {
		return nil, usage("%s não tem a ação de CLI %q (tem: %s)", nome, acao, strings.Join(e.CLI, ", "))
	}
	if args == nil {
		args = map[string]any{}
	}
	return chamarExtensao(c, base, proj.Root, nome, acao, args)
}
