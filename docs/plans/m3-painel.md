# Central M3 (painel) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans (execução nativa). Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Dar ao humano visibilidade ao vivo do que o time faz (spec §8). É um painel web local único, para todos os projetos da máquina, embutido no binário e servido pelo daemon em `http://127.0.0.1:<porta>/`.

**Architecture:**
- **Leitura:** o painel só lê pela API `/v1`. Esta etapa acrescenta os endpoints de leitura que faltam: lista de projetos com resumo, histórico de mensagens, eventos, amostras de recurso e um stream SSE por projeto.
- **Escrita:** o painel só escreve as três coisas da spec. A mensagem do humano usa o remetente reservado `HUMANO`; o veto e a resposta de decisão usam as rotas que já existem; a pasta de entrada fica para o M4.
- **Frontend:** HTML, CSS e JS sem framework, em `internal/web/` via `embed`. As rotas são de hash: `#/` é a máquina e `#/p/<id>/<aba>` é o projeto.

**Spec:** `docs/spec.md` §8.

## Global Constraints
- O daemon serve o painel só em loopback, como o resto. Nada é carregado de fora (sem CDN), então funciona offline.
- O SSE (`GET /v1/projetos/{id}/stream?depois=<seq>`) manda os eventos novos com `id: <seq>`, para o `EventSource` retomar sozinho, e um comentário de vida a cada 15 s.
- `HUMANO` passa a ser um nome reservado de vaga, como `TODOS` e `CENTRAL`.
- O painel atualiza a aba aberta quando chega um evento, com no máximo uma atualização por segundo.
- O painel funciona em tela estreita e tem tema claro e escuro (`prefers-color-scheme`).

## Tasks

### Task 1: API de leitura do painel
- `GET /v1/projetos` lista os projetos com um resumo de cada um: id, nome, raiz, último acesso, sprint aberta, contagem de vagas por estado, fila de QA e número de alertas da `saude`. Não abre projeto que não esteja aberto, salvo para o resumo (o `OpenAll` do daemon já abriu todos).
- `GET /v1/projetos/{id}/mensagens?depois=<seq>&limite=<n>` traz o histórico (todas as mensagens, sem cursor) e a lista de pendências (`responder_ate` em aberto). O limite padrão é 200.
- `GET /v1/projetos/{id}/eventos?depois=&tipos=&prefixos=&de=&ate=&limite=` lê `core.Events`.
- `GET /v1/projetos/{id}/recursos` traz o `ResourceStatus` de cada recurso.
- `GET /v1/projetos/{id}/recursos/{nome}/amostras?de=&ate=` traz as amostras e as concessões do período (`GrantsBetween`).
- `GET /v1/projetos/{id}/stream?depois=` é o SSE.
- [ ] Testes de API: `TestListaProjetosComResumo`, `TestHistoricoDeMensagens`, `TestEventosFiltrados`, `TestAmostrasEConcessoes`, `TestStreamMandaEventosNovos` (um evento criado depois da conexão chega em < 2 s, com `id:`).

### Task 2: Mensagem do humano
- `core.Enter` passa a recusar a vaga `HUMANO`.
- `POST /v1/projetos/{id}/humano/mensagens {para, tipo, corpo, ref}` vira `core.Send` com `De = HUMANO` e acorda o destino.
- [ ] Testes: `TestHumanoEnviaEAcorda` e `TestHumanoReservado`.

### Task 3: Frontend
- `internal/web/` com `index.html`, `app.css` e `app.js`; o daemon serve `/` e `/static/*`.
- Telas:
  1. **Máquina:** um cartão por projeto com nome, sprint (relógio), vagas por estado, fila de QA, alertas e último acesso.
  2. **Agora:** uma tabela de vagas (papel, estado com cor, status, reserva, batimento "há X min", sumido em vermelho) e os recursos (concessões ativas, fila, última leitura × capacidade).
  3. **Quadro:** colunas proposta, pronta, em andamento, concluída, em revisão, aprovada, integrada e validada. Cada cartão mostra id, título, dono e tempo no estado, e fica vermelho acima do limite do `[processo.limite_parado]`, que vem da `saude`.
  4. **Conversa:** um chat com as mensagens mais novas embaixo, filtro por vaga, papel ou item, pendências destacadas, mensagens de `CENTRAL` atenuadas e um formulário para mandar como `HUMANO`.
  5. **Linha do tempo:** um SVG com uma faixa por vaga colorida pelo estado (dos eventos `agente.*`) nas últimas 6 h, ou na janela da sprint. Por recurso, a série medida por dimensão com o declarado das concessões por cima.
  6. **Sprint:** horários das cerimônias com marcação de feitas e próxima, as métricas (`/metricas`) e o tamanho do brief por papel (eventos `brief.gerado`).
  7. **Decisões:** as decisões não finais, com corpo, notas e botões de veto e de resposta (aprovar ou recusar) para as escaladas.
- O stream SSE do projeto aberto atualiza a aba, com uma atualização por segundo no máximo.
- [ ] Teste Go: `TestPainelServidoEmbutido` (`/` devolve HTML com o elemento raiz, `/static/app.js` devolve JS, e um caminho desconhecido em `/static` dá 404).

### Task 4: `central painel`
- Garante o daemon e abre o navegador padrão em `http://<addr>/`. No Windows usa `rundll32 url.dll,FileProtocolHandler`, no macOS `open` e no resto `xdg-open`. Com `--so-url sim`, não abre o navegador e imprime a URL em JSON.
- [ ] Teste: `TestPainelSoURL`.

### Task 5: Verificação visual e documentação
- Sobe um daemon de teste com dados semeados pela CLI: projeto com sprint aberta, itens em todas as colunas, mensagens, uma decisão escalada, recurso com amostras e uma vaga sumida. Abre o painel no navegador embutido, tira captura de cada aba em largura de desktop e de celular, e corrige o que estiver quebrado ou ilegível.
- README: seção "Painel".
