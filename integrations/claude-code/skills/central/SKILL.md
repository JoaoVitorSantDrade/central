---
name: central
description: Como trabalhar num time de agentes coordenado pela Central (central.toml na raiz do projeto). Use ao começar uma sessão de agente num projeto com central.toml, ao pegar, concluir, revisar ou integrar tarefas, ao mandar ou ler mensagens entre agentes, ao pedir a GPU ou outro recurso, ao travar um arquivo compartilhado e sempre que for ficar ocioso esperando trabalho.
---

# Trabalhar com a Central

A Central guarda o estado vivo do time (quem faz o quê, mensagens, travas, recursos e sprint) e acorda o agente certo na hora. Você conversa com ela pelas ferramentas MCP `central` ou pela CLI `central` (as duas fazem a mesma coisa; a saída é JSON).

## Ao começar a sessão
1. Uma vez: `central entrar --vaga <VAGA> --papel <PAPEL>` na pasta do seu worktree (a mensagem que abriu a sessão diz a vaga e o papel).
2. `central brief --md`: quem você é, o que está acontecendo agora, suas pendências, o cartão do seu papel, o núcleo do projeto, as regras que valem para você, os fatos relevantes e o mapa de leitura. **Leia só o brief**, não manuais inteiros: o mapa diz quando abrir cada documento.
3. Depois de um resumo automático da conversa, `central brief --curto --md` (o plugin já faz isso sozinho num hook).

## Durante o trabalho
- Siga o cartão do seu papel (bloco 4 do brief). Os comandos mais comuns:
  - tarefas: `central proxima`, `central reservar <id>`, `central concluir <id> --ponta <hash>`, `central bloquear <id> "<nota>"`;
  - revisão e integração: `central revisar|aprovar|rejeitar|integrar <id> …`;
  - mensagens: `central caixa`, `central enviar <para> <tipo> "<texto>" [--responder 30m]`, `central responder <seq> "<texto>"`;
  - recursos e travas: `central rodar --rotulo "<o quê>" --recurso gpu --tipo <tipo> -- <comando>`, `central trava <nome> --motivo "<id>"`.
- Códigos de saída: 0 ok, 2 conflito (outro pegou antes, estado errado), 3 não encontrado, 4 recusado (papel errado, faltou campo), 5 daemon fora.
- Nunca grave o estado do time à mão em arquivos: use a Central.

## Sem trabalho (o mais importante)
Não fique dormindo por relógio. Rode `central esperar --ate 30m` pelo shell **em segundo plano** (Bash com `run_in_background: true`), agende um despertar de reserva de 30 min e termine o turno. Quando chegar algo para você (mensagem, tarefa para revisar, sua vez na GPU, uma cerimônia da sprint), o comando termina e você é re-invocado na hora: comece o ciclo por `central caixa`.

## Se a Central executa a sua vaga
Quando a mensagem que abriu a sessão diz "Você é a vaga … do projeto …" ou "Você acordou: …", quem conduz os seus turnos é a Central (`[execucao]` no `central.toml`). Aí:
- **não** rode `central esperar` nem agende despertares do app: ao terminar uma unidade de trabalho, termine o turno. A Central acorda você com um turno novo quando houver mensagem;
- para esperar algo que não gera mensagem (um CI, um processo longo), use `central despertar --em <duração> "<motivo>"` e termine o turno;
- cada turno começa por `central caixa`. Um pedido de permissão que ninguém pode responder é negado e registrado: siga por outro caminho ou avise o GERENTE.
- O GERENTE abre e fecha vagas com `central vaga abrir <VAGA> --papel <PAPEL> [--nota "<texto>"]` e `central vaga fechar <VAGA>`, dentro do teto do `central.toml`; `central vagas` mostra o estado.
