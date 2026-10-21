package executor

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// linhaStream é o que o executor lê de cada mensagem do stream-json.
type linhaStream struct {
	Type      string  `json:"type"`
	Subtype   string  `json:"subtype"`
	SessionID string  `json:"session_id"`
	Cwd       string  `json:"cwd"`
	IsError   bool    `json:"is_error"`
	Result    string  `json:"result"`
	CostUSD   float64 `json:"total_cost_usd"`
	NumTurns  int     `json:"num_turns"`
	Usage     struct {
		Input  int `json:"input_tokens"`
		Output int `json:"output_tokens"`
	} `json:"usage"`
	Denials []struct {
		ToolName  string          `json:"tool_name"`
		ToolInput json.RawMessage `json:"tool_input"`
	} `json:"permission_denials"`
}

// LerLog lê o log de um turno (o stream-json gravado) e devolve o que o Claude
// Code informou; ok diz se a mensagem result chegou. A falha vem só do result:
// limite do plano, erro ou ok. A recuperação usa isto num turno que terminou
// enquanto o daemon estava fora.
func LerLog(caminho string) (Resultado, bool) {
	r, _, ok := lerLog(caminho)
	return r, ok
}

// lerLog também devolve a linha crua do result, que o corte do log preserva.
func lerLog(caminho string) (Resultado, []byte, bool) {
	r := Resultado{Falha: FalhaErro}
	f, err := os.Open(caminho)
	if err != nil {
		return r, nil, false
	}
	defer f.Close()
	var ultimo []byte
	ok := false
	br := bufio.NewReaderSize(f, 64*1024)
	for {
		linha, err := br.ReadBytes('\n')
		if t := bytes.TrimSpace(linha); len(t) > 0 && t[0] == '{' {
			var m linhaStream
			if json.Unmarshal(t, &m) == nil {
				switch m.Type {
				case "system":
					if m.Subtype == "init" {
						r.Sessao, r.Pasta = m.SessionID, m.Cwd
					}
				case "result":
					ok = true
					ultimo = append(ultimo[:0], t...)
					aplicarResult(&r, m)
				}
			}
		}
		if err != nil {
			break
		}
	}
	return r, ultimo, ok
}

func aplicarResult(r *Resultado, m linhaStream) {
	if m.SessionID != "" {
		r.Sessao = m.SessionID
	}
	r.Texto = cauda(m.Result, MaxTexto)
	r.CustoUSD, r.TurnosModelo = m.CostUSD, m.NumTurns
	r.TokensEntrada, r.TokensSaida = m.Usage.Input, m.Usage.Output
	r.Negacoes = nil
	for _, d := range m.Denials {
		r.Negacoes = append(r.Negacoes, cauda(strings.TrimSpace(d.ToolName+" "+string(d.ToolInput)), 300))
	}
	r.ResetEm = nil
	switch {
	case m.IsError && limiteDoPlano(m.Result):
		r.Falha = FalhaLimitePlano
		if i := strings.LastIndex(m.Result, "|"); i >= 0 {
			if n, err := strconv.ParseInt(strings.TrimSpace(m.Result[i+1:]), 10, 64); err == nil && n > 0 {
				t := time.Unix(n, 0)
				r.ResetEm = &t
			}
		}
	case m.IsError:
		r.Falha = FalhaErro
	default:
		r.Falha = FalhaOK
	}
}

// limiteDoPlano reconhece a mensagem de limite de uso do plano do Claude.
func limiteDoPlano(txt string) bool {
	t := strings.ToLower(txt)
	return strings.Contains(t, "usage limit") || strings.Contains(t, "limit reached") || strings.Contains(t, "hit your limit")
}

// cauda devolve os últimos max bytes de s sem cortar um caractere ao meio.
func cauda(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[len(s)-max:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

// caudaArquivo lê os últimos max bytes de um arquivo.
func caudaArquivo(caminho string, max int64) string {
	f, err := os.Open(caminho)
	if err != nil {
		return ""
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > max {
		f.Seek(st.Size()-max, io.SeekStart)
	}
	b, _ := io.ReadAll(f)
	return cauda(strings.TrimSpace(string(b)), int(max))
}

// cortarLog limita o log a MaxLog bytes, preservando a linha do result no fim.
func cortarLog(caminho string, result []byte) error {
	st, err := os.Stat(caminho)
	if err != nil || st.Size() <= MaxLog {
		return err
	}
	f, err := os.OpenFile(caminho, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	aviso := []byte(fmt.Sprintf("\n{\"type\":\"central\",\"aviso\":\"log cortado em %d bytes\"}\n", MaxLog))
	corte := MaxLog - int64(len(aviso)+len(result)+1)
	if corte < 0 {
		return errors.New("result maior que o log")
	}
	if err := f.Truncate(corte); err != nil {
		return err
	}
	if _, err := f.Seek(corte, io.SeekStart); err != nil {
		return err
	}
	_, err = f.Write(append(append(aviso, result...), '\n'))
	return err
}
