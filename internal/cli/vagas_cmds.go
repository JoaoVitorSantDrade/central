package cli

import (
	"net/url"
	"strings"
)

// Comandos do executor de vagas: o GERENTE (ou o humano, numa pasta que não
// entrou) abre e fecha vagas; a vaga executada pede um turno com hora.
func init() {
	comandosProcesso["vaga abrir"] = comandoProcesso{flags: []string{"papel", "area", "nota"}, run: func(a processArgs) (map[string]any, error) {
		if len(a.pos) != 1 || a.flags["papel"] == "" {
			return nil, usage("vaga abrir precisa de <VAGA> --papel P [--area A] [--nota T]")
		}
		return a.post("/vagas", map[string]any{"vaga": a.pos[0], "papel": a.flags["papel"], "area": a.flags["area"], "nota": a.flags["nota"]})
	}}
	comandosProcesso["vaga fechar"] = comandoProcesso{run: func(a processArgs) (map[string]any, error) {
		if len(a.pos) != 1 {
			return nil, usage("vaga fechar precisa de <VAGA>")
		}
		return a.post("/vagas/"+url.PathEscape(a.pos[0])+"/fechar", nil)
	}}
	comandosProcesso["vagas"] = comandoProcesso{run: func(a processArgs) (map[string]any, error) {
		if len(a.pos) > 0 {
			return nil, usage("vagas não recebe argumentos")
		}
		return a.get("/vagas", nil)
	}}
	comandosProcesso["despertar"] = comandoProcesso{flags: []string{"em"}, run: func(a processArgs) (map[string]any, error) {
		motivo := strings.TrimSpace(strings.Join(a.pos, " "))
		if a.flags["em"] == "" || motivo == "" {
			return nil, usage(`despertar precisa de --em <duração> "<motivo>" (ex.: --em 20m "ver o CI")`)
		}
		return a.post("/despertar", map[string]any{"em": a.flags["em"], "motivo": motivo})
	}}
}
