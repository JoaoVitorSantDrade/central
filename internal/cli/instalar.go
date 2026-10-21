package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/JoaoVitorSantDrade/central/internal/config"
)

// Plugin do Claude Code instalado junto com a Central.
const (
	pluginMarketplace = "JoaoVitorSantDrade/central"
	pluginNome        = "central@central"
)

// pathStore lê e grava o PATH do usuário (o registro no Windows; um arquivo
// nos testes, por CENTRAL_TESTE_PATH).
type pathStore interface {
	Ler() (string, error)
	Gravar(string) error
}

// errSemGravarPath é a resposta do PATH que não se grava (fora do Windows).
var errSemGravarPath = errors.New("sem PATH persistente fora do Windows")

type pathArquivo string

func (p pathArquivo) Ler() (string, error) {
	b, err := os.ReadFile(string(p))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

func (p pathArquivo) Gravar(v string) error { return os.WriteFile(string(p), []byte(v), 0o644) }

func lojaPath() pathStore {
	if f := os.Getenv("CENTRAL_TESTE_PATH"); f != "" {
		return pathArquivo(f)
	}
	return pathUsuario{}
}

func sepPath(windows bool) string {
	if windows {
		return ";"
	}
	return ":"
}

// mesmaPasta compara entradas do PATH: no Windows sem caixa e sem barra final.
func mesmaPasta(a, b string, windows bool) bool {
	limpa := func(s string) string { return strings.TrimRight(strings.TrimSpace(s), `\/`) }
	a, b = limpa(a), limpa(b)
	if windows {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// pathCom devolve o PATH com dir no fim, sem duplicar.
func pathCom(path, dir string, windows bool) string {
	sep := sepPath(windows)
	for _, p := range strings.Split(path, sep) {
		if mesmaPasta(p, dir, windows) {
			return path
		}
	}
	if strings.TrimSpace(path) == "" {
		return dir
	}
	return strings.TrimRight(path, sep) + sep + dir
}

// pathSem devolve o PATH sem nenhuma entrada igual a dir.
func pathSem(path, dir string, windows bool) string {
	sep := sepPath(windows)
	var out []string
	mudou := false
	for _, p := range strings.Split(path, sep) {
		if mesmaPasta(p, dir, windows) && strings.TrimSpace(p) != "" {
			mudou = true
			continue
		}
		out = append(out, p)
	}
	if !mudou {
		return path
	}
	return strings.Join(out, sep)
}

func pastaInstalacao() (string, error) {
	if d := os.Getenv("CENTRAL_INSTALAR_EM"); d != "" {
		return filepath.Abs(d)
	}
	return pastaInstalacaoPadrao()
}

func nomeBinario() string {
	if runtime.GOOS == "windows" {
		return "central.exe"
	}
	return "central"
}

func esteExecutavel() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// pararSeRodando encerra o daemon desta pasta de dados, se houver.
func pararSeRodando() (bool, error) {
	c := newClient(config.Addr())
	info, ok := c.saude()
	if !ok {
		return false, nil
	}
	return true, c.encerrarDaemon(info)
}

// copiarExecutavel grava src em dst trocando de uma vez; um dst em uso (o
// Windows não sobrescreve um .exe aberto) é antes renomeado para .antigo.
func copiarExecutavel(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".novo"
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return trocarArquivo(tmp, dst)
}

// trocarArquivo põe novo no lugar de dst; se dst está em uso, ele vira .antigo.
func trocarArquivo(novo, dst string) error {
	if err := os.Rename(novo, dst); err == nil {
		return nil
	}
	antigo := dst + ".antigo"
	os.Remove(antigo)
	if err := os.Rename(dst, antigo); err != nil && !errors.Is(err, os.ErrNotExist) {
		os.Remove(novo)
		return fmt.Errorf("não consegui tirar %s do lugar: %w", dst, err)
	}
	if err := os.Rename(novo, dst); err != nil {
		os.Rename(antigo, dst)
		return err
	}
	return nil
}

// rodarClaude roda um comando do Claude Code e devolve o resumo para o relatório.
func rodarClaude(args ...string) string {
	claude, err := exec.LookPath("claude")
	if err != nil {
		return "pulado: o comando claude não está no PATH (instale o plugin pelo Claude Code: /plugin marketplace add " + pluginMarketplace + ")"
	}
	out, err := exec.Command(claude, args...).CombinedOutput()
	txt := strings.TrimSpace(string(out))
	if len(txt) > 300 {
		txt = txt[len(txt)-300:]
	}
	if err != nil {
		return fmt.Sprintf("aviso: claude %s falhou (%v): %s", strings.Join(args, " "), err, txt)
	}
	return "ok"
}

func simNao(flags map[string]string, nome string) (bool, error) {
	switch flags[nome] {
	case "", "nao", "não":
		return false, nil
	case "sim":
		return true, nil
	}
	return false, usage("--%s aceita sim ou nao", nome)
}

// runInstalar instala este binário para o usuário: pasta própria, PATH e plugin.
func runInstalar(rest []string) (map[string]any, error) {
	flags, pos, err := parseArgs(rest, "sem-plugin")
	if err != nil {
		return nil, err
	}
	if len(pos) > 0 {
		return nil, usage("instalar não recebe argumentos")
	}
	semPlugin, err := simNao(flags, "sem-plugin")
	if err != nil {
		return nil, err
	}
	dir, err := pastaInstalacao()
	if err != nil {
		return nil, err
	}
	src, err := esteExecutavel()
	if err != nil {
		return nil, err
	}
	dst := filepath.Join(dir, nomeBinario())
	out := map[string]any{"ok": true, "pasta": dir, "binario": dst}
	parou, err := pararSeRodando()
	if err != nil {
		return nil, err
	}
	out["daemon_parado"] = parou
	if real, err := filepath.EvalSymlinks(dst); err == nil && samePath(real, src) {
		out["copia"] = "já roda de lá"
	} else {
		if err := copiarExecutavel(src, dst); err != nil {
			return nil, newErr(ExitErro, "instalar", "copiar para %s: %v", dst, err)
		}
		out["copia"] = "ok"
	}
	loja := lojaPath()
	atual, err := loja.Ler()
	if err != nil {
		return nil, newErr(ExitErro, "instalar", "ler o PATH: %v", err)
	}
	novo := pathCom(atual, dir, pathWindows)
	switch {
	case novo == atual:
		out["path"] = "já estava"
	default:
		if err := loja.Gravar(novo); errors.Is(err, errSemGravarPath) {
			out["path"] = fmt.Sprintf("acrescente %s ao PATH do seu shell (ex.: no ~/.profile: export PATH=\"$PATH:%s\")", dir, dir)
		} else if err != nil {
			return nil, newErr(ExitErro, "instalar", "gravar o PATH: %v", err)
		} else {
			out["path"] = "acrescentado (abra um terminal novo)"
		}
	}
	if semPlugin {
		out["plugin"] = "pulado (--sem-plugin)"
	} else {
		r := rodarClaude("plugin", "marketplace", "add", pluginMarketplace)
		if r == "ok" {
			r = rodarClaude("plugin", "install", pluginNome)
		}
		out["plugin"] = r
	}
	return out, nil
}

// runDesinstalar desfaz o instalar; os dados (mensagens, tarefas, fatos) ficam.
func runDesinstalar(rest []string) (map[string]any, error) {
	flags, pos, err := parseArgs(rest, "sem-plugin")
	if err != nil {
		return nil, err
	}
	if len(pos) > 0 {
		return nil, usage("desinstalar não recebe argumentos")
	}
	semPlugin, err := simNao(flags, "sem-plugin")
	if err != nil {
		return nil, err
	}
	dir, err := pastaInstalacao()
	if err != nil {
		return nil, err
	}
	dst := filepath.Join(dir, nomeBinario())
	dados, _ := config.DataDir()
	out := map[string]any{"ok": true, "pasta": dir, "dados": dados,
		"aviso": "os dados ficaram em " + dados + "; apague a pasta à mão se não quiser mais"}
	parou, err := pararSeRodando()
	if err != nil {
		return nil, err
	}
	out["daemon_parado"] = parou
	loja := lojaPath()
	if atual, err := loja.Ler(); err == nil {
		if novo := pathSem(atual, dir, pathWindows); novo != atual {
			if err := loja.Gravar(novo); err == nil {
				out["path"] = "removido"
			}
		}
	}
	if _, ok := out["path"]; !ok {
		out["path"] = "não estava"
	}
	out["binario"] = removerBinario(dst)
	if semPlugin {
		out["plugin"] = "pulado (--sem-plugin)"
	} else {
		out["plugin"] = rodarClaude("plugin", "uninstall", pluginNome)
	}
	return out, nil
}

// removerBinario apaga o binário instalado. Se é o que está rodando (Windows não
// apaga um .exe aberto), um cmd separado o apaga logo depois que este processo sai.
func removerBinario(dst string) string {
	err := os.Remove(dst)
	switch {
	case err == nil:
		os.Remove(dst + ".antigo")
		return "removido"
	case errors.Is(err, os.ErrNotExist):
		return "não estava"
	case runtime.GOOS == "windows":
		cmd := exec.Command("cmd", "/c", "ping -n 3 127.0.0.1 >nul & del /f /q \""+dst+"\" \""+dst+".antigo\"")
		detach(cmd, false)
		if cmd.Start() == nil {
			cmd.Process.Release()
			return "removido ao sair"
		}
	}
	return "não consegui remover: " + err.Error()
}
