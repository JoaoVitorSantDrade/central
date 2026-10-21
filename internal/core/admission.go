package core

import (
	"maps"
	"slices"
	"time"
)

// SortQueue ordena a fila pela prioridade (a ordem de spec.Prioridades; uma
// desconhecida vai para o fim), depois pela chegada e depois pelo ID.
func SortQueue(spec ResourceSpec, fila []Grant) {
	rank := func(p string) int {
		if i := slices.Index(spec.Prioridades, p); i >= 0 {
			return i
		}
		return len(spec.Prioridades)
	}
	slices.SortStableFunc(fila, func(a, b Grant) int {
		if ra, rb := rank(a.Prioridade), rank(b.Prioridade); ra != rb {
			return ra - rb
		}
		if c := a.PedidoEm.Compare(b.PedidoEm); c != 0 {
			return c
		}
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})
}

// AdmissionInput é o estado que a admissão olha. Medido nil quer dizer sem
// leitura recente do medidor: vale só a conta declarada.
type AdmissionInput struct {
	Spec   ResourceSpec
	Ativas []Grant
	Fila   []Grant
	Medido Dims
	Now    time.Time
}

// Admit devolve os IDs da fila que entram agora, em ordem (spec §5.3). Simula a
// entrada de um por vez: cada um que entra soma o declarado às ativas e o pedido
// ao medido. A cabeça entra se couber; depois que um não cabe, ele vira a cabeça
// que espera, e os seguintes só entram (backfill) se couberem e terminarem até o
// início estimado dela. Função pura: não lê relógio nem banco.
//
// O início estimado só olha a conta declarada (EstimatedStart): quando a cabeça
// cabe nela e só a medida a barra, o início estimado é Now e nenhum pedido de
// prazo positivo passa no backfill até a medida baixar. É de propósito: a
// leitura não diz quando o uso vai cair, e deixar entrar os menores poderia
// adiar a cabeça indefinidamente.
func Admit(in AdmissionInput) []int64 {
	fila := slices.Clone(in.Fila)
	SortQueue(in.Spec, fila)
	ativas := slices.Clone(in.Ativas)
	var medido Dims
	if in.Medido != nil {
		medido = maps.Clone(in.Medido)
	}
	ids := []int64{}
	var inicio *time.Time // início estimado da cabeça que não coube
	for _, g := range fila {
		if !fits(in.Spec, ativas, medido, g) {
			if inicio == nil {
				t := EstimatedStart(in.Spec, ativas, g, in.Now)
				inicio = &t
			}
			continue
		}
		if inicio != nil && in.Now.Add(g.Duracao).After(*inicio) {
			continue
		}
		ids = append(ids, g.ID)
		ini, prazo := in.Now, in.Now.Add(g.Duracao)
		g.Inicio, g.Prazo = &ini, &prazo
		ativas = append(ativas, g)
		if medido != nil {
			for d, v := range g.Declarado {
				medido[d] += v
			}
		}
	}
	return ids
}

// fits diz se g cabe agora: as contas exclusiva e declarada e, com medido, a medida.
// Só contam as dimensões que g declara (mais o teto, que vale para todas).
func fits(spec ResourceSpec, ativas []Grant, medido Dims, g Grant) bool {
	if !fitsDeclared(spec, ativas, g) {
		return false
	}
	if medido == nil {
		return true
	}
	for d, v := range g.Declarado {
		if medido[d]+v+spec.Margem[d] > spec.Dimensoes[d] {
			return false
		}
	}
	for d, teto := range spec.TetoMedido {
		if medido[d] >= teto*spec.Dimensoes[d] {
			return false
		}
	}
	return true
}

// fitsDeclared é a conta exclusiva e a declarada: nenhuma ativa exclusiva; g
// exclusivo só sem ativas; e, por dimensão de g, soma das ativas + g ≤ dimensão − reservado.
func fitsDeclared(spec ResourceSpec, ativas []Grant, g Grant) bool {
	if g.Exclusivo && len(ativas) > 0 {
		return false
	}
	for _, a := range ativas {
		if a.Exclusivo {
			return false
		}
	}
	for d, v := range g.Declarado {
		soma := 0.0
		for _, a := range ativas {
			soma += a.Declarado[d]
		}
		if soma+v > spec.Dimensoes[d]-spec.Reservado[d] {
			return false
		}
	}
	return true
}

// EstimatedStart estima quando head caberia na conta declarada: libera as ativas
// em ordem de prazo até caber e devolve o prazo da última liberada (now se já
// cabe; nunca antes de now). Para head exclusiva isso é o maior prazo das ativas.
// Uma ativa sem prazo conta como vencendo agora.
func EstimatedStart(spec ResourceSpec, ativas []Grant, head Grant, now time.Time) time.Time {
	restantes := slices.Clone(ativas)
	prazo := func(g Grant) time.Time {
		if g.Prazo == nil {
			return now
		}
		return *g.Prazo
	}
	slices.SortStableFunc(restantes, func(a, b Grant) int { return prazo(a).Compare(prazo(b)) })
	inicio := now
	for len(restantes) > 0 && !fitsDeclared(spec, restantes, head) {
		if p := prazo(restantes[0]); p.After(inicio) {
			inicio = p
		}
		restantes = restantes[1:]
	}
	return inicio
}
