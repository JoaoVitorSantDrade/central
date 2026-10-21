package cli

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/JoaoVitorSantDrade/central/internal/version"
)

// Releases da Central no GitHub (CENTRAL_RELEASES troca a base, para testes).
const (
	releasesBase  = "https://api.github.com"
	releasesRepo  = "JoaoVitorSantDrade/central"
	maxDownload   = 200 << 20
	prazoDownload = 5 * time.Minute
)

type release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Nome string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (r release) asset(nome string) string {
	for _, a := range r.Assets {
		if a.Nome == nome {
			return a.URL
		}
	}
	return ""
}

func nomeAsset() string {
	n := fmt.Sprintf("central-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		n += ".exe"
	}
	return n
}

func baixar(hc *http.Client, url string, w io.Writer) error {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "central/"+version.Versao)
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, maxDownload+1))
	if err == nil && n > maxDownload {
		err = fmt.Errorf("%s: maior que %d MB", url, maxDownload>>20)
	}
	return err
}

// buscarRelease lê a última release (ou a da versão pedida).
func buscarRelease(hc *http.Client, versao string) (release, error) {
	base := strings.TrimRight(os.Getenv("CENTRAL_RELEASES"), "/")
	if base == "" {
		base = releasesBase
	}
	url := base + "/repos/" + releasesRepo + "/releases/latest"
	if versao != "" {
		url = base + "/repos/" + releasesRepo + "/releases/tags/v" + strings.TrimPrefix(versao, "v")
	}
	var buf strings.Builder
	if err := baixar(hc, url, &buf); err != nil {
		return release{}, err
	}
	var r release
	if err := json.Unmarshal([]byte(buf.String()), &r); err != nil {
		return release{}, fmt.Errorf("resposta da API de releases: %v", err)
	}
	return r, nil
}

// somaEsperada acha no SHA256SUMS a soma do arquivo nome.
func somaEsperada(sums, nome string) string {
	sc := bufio.NewScanner(strings.NewReader(sums))
	for sc.Scan() {
		campos := strings.Fields(sc.Text())
		if len(campos) == 2 && strings.TrimPrefix(campos[1], "*") == nome {
			return strings.ToLower(campos[0])
		}
	}
	return ""
}

// runAtualizar baixa a release, confere a soma e troca este executável.
func runAtualizar(rest []string) (map[string]any, error) {
	flags, pos, err := parseArgs(rest, "versao")
	if err != nil {
		return nil, err
	}
	if len(pos) > 0 {
		return nil, usage("atualizar não recebe argumentos (use --versao X.Y.Z)")
	}
	exe, err := esteExecutavel()
	if err != nil {
		return nil, err
	}
	os.Remove(exe + ".antigo") // sobra da atualização anterior
	hc := &http.Client{Timeout: prazoDownload}
	r, err := buscarRelease(hc, flags["versao"])
	if err != nil {
		return nil, newErr(ExitErro, "atualizar", "não consegui ler a release: %v", err)
	}
	alvo := strings.TrimPrefix(r.Tag, "v")
	if _, ok := partesSemver(alvo); !ok {
		return nil, newErr(ExitErro, "atualizar", "a release tem a tag %q, que não é semver", r.Tag)
	}
	if flags["versao"] == "" && compararVersoes(alvo, version.Versao) <= 0 {
		return map[string]any{"ok": true, "atualizado": false, "versao": version.Versao, "ultima": alvo}, nil
	}
	nome := nomeAsset()
	urlBin, urlSums := r.asset(nome), r.asset("SHA256SUMS")
	if urlBin == "" || urlSums == "" {
		return nil, newErr(ExitErro, "atualizar", "a release %s não tem %s ou SHA256SUMS", r.Tag, nome)
	}
	var sums strings.Builder
	if err := baixar(hc, urlSums, &sums); err != nil {
		return nil, newErr(ExitErro, "atualizar", "baixar SHA256SUMS: %v", err)
	}
	esperada := somaEsperada(sums.String(), nome)
	if esperada == "" {
		return nil, newErr(ExitErro, "atualizar", "o SHA256SUMS da release não lista %s", nome)
	}
	novo := exe + ".novo"
	f, err := os.OpenFile(novo, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	err = baixar(hc, urlBin, io.MultiWriter(f, h))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(novo)
		return nil, newErr(ExitErro, "atualizar", "baixar %s: %v", nome, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != esperada {
		os.Remove(novo)
		return nil, newErr(ExitErro, "atualizar", "o SHA256 de %s não confere (esperado %s, baixado %s): nada foi trocado", nome, esperada, got)
	}
	// O binário novo precisa responder a própria versão antes de entrar no lugar.
	out, err := exec.Command(novo, "versao").Output()
	if err != nil || !strings.Contains(string(out), `"versao":"`+alvo+`"`) {
		os.Remove(novo)
		return nil, newErr(ExitErro, "atualizar", "o binário baixado não respondeu a versão %s (%s %v)", alvo, strings.TrimSpace(string(out)), err)
	}
	if _, err := pararSeRodando(); err != nil {
		os.Remove(novo)
		return nil, err
	}
	if err := trocarArquivo(novo, exe); err != nil {
		return nil, newErr(ExitErro, "atualizar", "trocar %s: %v", exe, err)
	}
	return map[string]any{"ok": true, "atualizado": true, "de": version.Versao, "para": alvo, "binario": exe}, nil
}
