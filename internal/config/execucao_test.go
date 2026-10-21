package config

import (
	"strings"
	"testing"
	"time"
)

const baseExecucao = `
[projeto]
id = "demo"
`

func TestExecucaoPadroesETraducao(t *testing.T) {
	c, err := ParseProjectConfig([]byte(baseExecucao + `
[papeis]
nomes = { EXECUTOR = "FIXER" }

[execucao.cargos]
FIXER   = { max = 2, modelo = "opus", esforco = "high" }
GERENTE = { max = 1, modelo = "sonnet", esforco = "medium", prazo_turno = "30m" }
`))
	if err != nil {
		t.Fatal(err)
	}
	e := c.Execucao
	if e == nil {
		t.Fatal("Execucao == nil")
	}
	if e.Claude != "claude" || e.MaxSimultaneos != 4 || e.PrazoTurno.Duration != 2*time.Hour {
		t.Fatalf("padrões = %+v", e)
	}
	x, ok := e.Cargo("EXECUTOR")
	if !ok || x.Max != 2 || x.Modelo != "opus" || x.Esforco != "high" || x.PrazoTurno.Duration != 2*time.Hour {
		t.Fatalf("Cargo(EXECUTOR) = %+v, %v", x, ok)
	}
	g, ok := e.Cargo("GERENTE")
	if !ok || g.PrazoTurno.Duration != 30*time.Minute {
		t.Fatalf("Cargo(GERENTE) = %+v, %v", g, ok)
	}
	if _, ok := e.Cargo("REVISOR"); ok {
		t.Fatal("REVISOR sem entrada não pode ser aberto")
	}
	if _, ok := e.Cargo("FIXER"); ok {
		t.Fatal("Cargo recebe o canônico, não o nome do projeto")
	}
}

func TestExecucaoInvalida(t *testing.T) {
	casos := []struct{ nome, toml, campo string }{
		{"esforco", `[execucao.cargos]
EXECUTOR = { max = 1, modelo = "opus", esforco = "turbo" }`, "esforco"},
		{"max", `[execucao.cargos]
EXECUTOR = { max = 0, modelo = "opus", esforco = "high" }`, "max"},
		{"modelo", `[execucao.cargos]
EXECUTOR = { max = 1, modelo = "", esforco = "high" }`, "modelo"},
		{"prazo", `[execucao]
prazo_turno = "10s"`, "prazo_turno"},
		{"prazo do cargo", `[execucao.cargos]
EXECUTOR = { max = 1, modelo = "opus", esforco = "high", prazo_turno = "13h" }`, "prazo_turno"},
		{"cargo", `[execucao.cargos]
FAXINEIRO = { max = 1, modelo = "opus", esforco = "high" }`, "cargos"},
		{"custo", `[execucao]
custo_dia_usd = -1`, "custo_dia_usd"},
		{"simultaneos", `[execucao]
max_simultaneos = -2`, "max_simultaneos"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			_, err := ParseProjectConfig([]byte(baseExecucao + c.toml))
			if err == nil {
				t.Fatal("esperava erro")
			}
			if !strings.Contains(err.Error(), "execucao") || !strings.Contains(err.Error(), c.campo) {
				t.Fatalf("erro não cita o campo %s: %v", c.campo, err)
			}
		})
	}
}

func TestSemExecucao(t *testing.T) {
	c, err := ParseProjectConfig([]byte(baseExecucao))
	if err != nil {
		t.Fatal(err)
	}
	if c.Execucao != nil {
		t.Fatalf("Execucao = %+v", c.Execucao)
	}
}
