package process

// Tipos de item.
const (
	TipoTarefa  = "tarefa"
	TipoMissao  = "missao"
	TipoDecisao = "decisao"
)

// Estados de tarefa e missão (spec §6.2).
const (
	Proposta    = "proposta"
	Pronta      = "pronta"
	EmAndamento = "em_andamento"
	Concluida   = "concluida"
	Bloqueada   = "bloqueada"
	EmRevisao   = "em_revisao"
	Aprovada    = "aprovada"
	Rejeitada   = "rejeitada"
	Integrada   = "integrada"
	Validada    = "validada"
)

// Estados de decisão (spec §6.2, decisao).
const (
	Rascunho = "rascunho"
	Chave1   = "chave1"
	Objecao  = "objecao"
	AHumano  = "humano"
	Vetada   = "vetada"
	// Aprovada também vale para decisão.
)
