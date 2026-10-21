package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/config"
	"github.com/JoaoVitorSantDrade/central/internal/ext"
	"github.com/JoaoVitorSantDrade/central/internal/service"
	"github.com/JoaoVitorSantDrade/central/internal/version"
)

// verificacao é um item do doctor.
type verificacao struct {
	Nome    string `json:"nome"`
	OK      bool   `json:"ok"`
	Detalhe string `json:"detalhe"`
}

// runDoctor confere a instalação sem subir o daemon e devolve o código 0 se
// tudo está ok, 1 senão.
func runDoctor(rest []string) (map[string]any, int, error) {
	if _, pos, err := parseArgs(rest); err != nil {
		return nil, 0, err
	} else if len(pos) > 0 {
		return nil, 0, usage("doctor não recebe argumentos")
	}
	addr := config.Addr()
	dataDir, dataErr := config.DataDir()
	if dataErr == nil {
		dataDir, dataErr = filepath.Abs(dataDir)
	}

	vs := []verificacao{}
	respondeu, v := checkDaemon(addr, dataDir, dataErr)
	vs = append(vs, v, checkDados(dataDir, dataErr))
	vs = append(vs, checkProjeto(dataDir)...)
	vs = append(vs, checkPorta(addr, respondeu))

	ok := true
	for _, v := range vs {
		ok = ok && v.OK
	}
	code := ExitOK
	if !ok {
		code = ExitErro
	}
	return map[string]any{"ok": ok, "verificacoes": vs}, code, nil
}

// checkDaemon pede a saúde ao daemon e confere a versão e a pasta de dados.
// Não sobe o daemon. Diz também se algum daemon respondeu.
func checkDaemon(addr, dataDir string, dataErr error) (bool, verificacao) {
	v := verificacao{Nome: "daemon"}
	hc := &http.Client{Timeout: time.Second}
	resp, err := hc.Get("http://" + addr + "/v1/saude")
	if err != nil {
		v.Detalhe = fmt.Sprintf("não responde em %s (os comandos o sobem sozinhos)", addr)
		return false, v
	}
	defer resp.Body.Close()
	var s struct {
		Versao string `json:"versao"`
		Dados  string `json:"dados"`
		PID    int    `json:"pid"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&s) != nil || s.Versao == "" {
		v.Detalhe = fmt.Sprintf("%s responde, mas não é um daemon da Central (%d)", addr, resp.StatusCode)
		return false, v
	}
	switch {
	case s.Versao != version.Versao:
		dica := "o próximo comando troca o daemon pelo desta versão"
		if compararVersoes(version.Versao, s.Versao) < 0 {
			dica = "este binário é mais antigo: atualize com central atualizar"
		}
		v.Detalhe = fmt.Sprintf("o daemon (pid %d) é da versão %s e este binário da %s (%s)", s.PID, s.Versao, version.Versao, dica)
	case dataErr != nil:
		v.Detalhe = fmt.Sprintf("pasta de dados desta CLI: %v", dataErr)
	case !samePath(dataDir, s.Dados):
		v.Detalhe = fmt.Sprintf("o daemon (pid %d) usa a pasta de dados %s, mas esta CLI usa %s", s.PID, s.Dados, dataDir)
	default:
		v.OK = true
		v.Detalhe = fmt.Sprintf("versão %s, pid %d, em %s", s.Versao, s.PID, addr)
	}
	return true, v
}

// checkDados confere que a pasta de dados existe e aceita escrita.
func checkDados(dataDir string, dataErr error) verificacao {
	v := verificacao{Nome: "dados"}
	if dataErr != nil {
		v.Detalhe = dataErr.Error()
		return v
	}
	st, err := os.Stat(dataDir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// Máquina nova: o daemon cria a pasta ao subir; basta o pai aceitar escrita.
		pai := filepath.Dir(dataDir)
		f, perr := os.CreateTemp(pai, ".doctor-*")
		if perr != nil {
			v.Detalhe = fmt.Sprintf("%s não existe e %s não aceita escrita: %v", dataDir, pai, perr)
			return v
		}
		f.Close()
		os.Remove(f.Name())
		v.OK = true
		v.Detalhe = fmt.Sprintf("%s ainda não existe; o daemon a cria ao subir", dataDir)
		return v
	case err != nil:
		v.Detalhe = err.Error()
		return v
	case !st.IsDir():
		v.Detalhe = fmt.Sprintf("%s não é uma pasta", dataDir)
		return v
	}
	f, err := os.CreateTemp(dataDir, ".doctor-*")
	if err != nil {
		v.Detalhe = fmt.Sprintf("%s não aceita escrita: %v", dataDir, err)
		return v
	}
	f.Close()
	os.Remove(f.Name())
	v.OK = true
	v.Detalhe = dataDir
	return v
}

// checkProjeto acha o central.toml a partir da pasta atual, valida a
// configuração e roda uma vez cada medidor (medidor:<recurso>), na mesma
// pasta em que o daemon o roda (service.MeterDir).
func checkProjeto(dataDir string) []verificacao {
	v := verificacao{Nome: "projeto"}
	cwd, err := os.Getwd()
	if err != nil {
		v.Detalhe = err.Error()
		return []verificacao{v}
	}
	proj, err := config.FindProject(cwd)
	if err != nil {
		v.Detalhe = err.Error()
		return []verificacao{v}
	}
	path := filepath.Join(proj.Root, config.FileName)
	texto, err := os.ReadFile(path)
	if err != nil {
		v.Detalhe = err.Error()
		return []verificacao{v}
	}
	cfg, err := config.ParseProjectConfig(texto)
	if err != nil {
		v.Detalhe = fmt.Sprintf("%s: %v", path, err)
		return []verificacao{v}
	}
	v.OK = true
	v.Detalhe = fmt.Sprintf("%s (%s)", proj.ID, path)
	vs := []verificacao{v}
	for _, nome := range slices.Sorted(maps.Keys(cfg.Recursos)) {
		m := cfg.Recursos[nome].Medidor
		if m == nil {
			continue
		}
		mv := verificacao{Nome: "medidor:" + nome}
		dims, err := service.RunMeter(context.Background(), service.MeterDir(proj.Root, dataDir), m.Comando, m.Mapa, service.MeterTimeout)
		if err != nil {
			mv.Detalhe = fmt.Sprintf("%q: %v", m.Comando, err)
		} else {
			b, _ := json.Marshal(dims)
			mv.OK, mv.Detalhe = true, string(b)
		}
		vs = append(vs, mv)
	}
	for _, e := range cfg.Extensoes {
		ev := verificacao{Nome: "extensao:" + e.Nome}
		if caminho, err := ext.Localizar(e.Comando, proj.Root); err != nil {
			ev.Detalhe = fmt.Sprintf("%q: %v", e.Comando, err)
		} else {
			ev.OK, ev.Detalhe = true, caminho
		}
		vs = append(vs, ev)
	}
	if cfg.Execucao != nil {
		vs = append(vs, checkExecutor(cfg.Execucao.Claude, service.MeterDir(proj.Root, dataDir))...)
	}
	return vs
}

// checkExecutor confere o Claude Code do [execucao]: o comando resolve
// (executor) e tem login (executor:login, `claude auth status` sai com 0).
func checkExecutor(claude, dir string) []verificacao {
	v := verificacao{Nome: "executor"}
	login := verificacao{Nome: "executor:login"}
	caminho, err := exec.LookPath(claude)
	if err != nil {
		v.Detalhe = fmt.Sprintf("%q não resolve (instale o Claude Code CLI ou ajuste [execucao] claude): %v", claude, err)
		login.Detalhe = "sem o comando do Claude Code"
		return []verificacao{v, login}
	}
	v.OK, v.Detalhe = true, caminho
	if err := service.RodarClaudeAuth(context.Background(), claude, dir); err != nil {
		login.Detalhe = err.Error()
	} else {
		login.OK, login.Detalhe = true, fmt.Sprintf("%q auth status ok", claude)
	}
	return []verificacao{v, login}
}

// checkPorta confere a porta da API. Com o daemon respondendo, ela é dele;
// senão, ela precisa estar livre para o daemon subir.
func checkPorta(addr string, respondeu bool) verificacao {
	v := verificacao{Nome: "porta"}
	if respondeu {
		v.OK = true
		v.Detalhe = fmt.Sprintf("%s em uso pelo daemon", addr)
		return v
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		v.Detalhe = fmt.Sprintf("%s ocupada por outro processo ou indisponível: %v (use outro CENTRAL_ADDR)", addr, err)
		return v
	}
	l.Close()
	v.OK = true
	v.Detalhe = fmt.Sprintf("%s livre", addr)
	return v
}
