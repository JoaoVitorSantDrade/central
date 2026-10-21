# M1b: decisões e pendências

Extraído do ledger do plano [m1b-processo](../plans/m1b-processo.md). Execução nativa, sem subagentes (pedido do humano); revisão final feita pelo controlador.

## Decisões (Ruling)

- Pre-flight: T1 (Roles/config) → T3,T4,T5,T6; T2 (WithItems/Item) → T3..T9; T5 (sprint no core + AddTickHook) → T3 (congelamento lido da sprint: até T5 existir, sem sprint = não congelado), T4, T8; T7 depende de T3 (Sincronizar usa a mesma gravação); T9 depende de T2/T5 (ImportItem/sprint). Migração 4 editada incrementalmente até o merge — Ruling: igual ao M1a (nada publicado antes do merge) — custo: nenhum.
- Ruling: ordem de execução T1, T2, T5, T3, T4, T6, T7, T8, T9, T10 — a T3 lê o congelamento da sprint, que a T5 cria — custo: nenhum
- Task 3: Ruling: ações via um despachante Engine.Acao (uma rota POST .../itens/{id}/{acao}) em vez de um método por ação — menos superfície na API — custo: nenhum
- Task 3: Ruling: verificação de estado antes do papel (estado errado → 2; papel errado → 4); quem não é dono → 4 — custo: só código de saída
- Task 3: Ruling: missão volta a em_andamento a cada marco integrado; integrar --final sim fecha — custo: um flag a mais
- Task 3: Ruling: deadlock achado (método do Core dentro de WithItems com uma conexão só): ItemTx.Sprint() e aviso no doc de WithItems — custo: nenhum
- Task 4: Ruling: proxima só para tarefas (missões são reservadas pelo id); recurso saturado = fila não vazia ou exclusiva ativa, aplicado depois do grupo; aviso fila.vazia limitado por evento (sobrevive a reinício) — custo: nenhum
- Task 6: Ruling: segunda rodada depois da objeção (decisao revisar) e segundo desacordo → humano automático, como no manual do LyricCut §2; veto e resposta do humano não exigem vaga (rota sem pasta) — custo: confiança local, como o resto da Central
- Task 7: Ruling: intervalo do adaptador configurável por projeto ([itens.arquivos] intervalo, padrão 30s, mínimo 1s) e loop do daemon a cada 1s — permite e2e rápido e projetos com ritmos diferentes — custo: um campo a mais na config
- Task 7: Ruling: avançar só proposta→pronta e qualquer→integrada; campos descritivos do arquivo sempre atualizam (mesmo com o item reservado); campos do processo (revisor, validacao) preservados — custo: título/prioridade editados no daemon são sobrescritos pelo arquivo
- Task 8: Ruling: saude não lista concessões sem uso (o aviso recurso.sem_uso já vai ao dono e o Grant não guarda o flag) — custo: o painel precisa ler o evento para destacar
- Task 8: Ruling: LiberarOrfas só devolve a pronta itens em_andamento/rejeitada; concluída/em revisão/aprovada órfãs só são listadas (podem seguir sem o dono) — custo: alguém precisa decidir essas à mão
- Task 9: Ruling: histórico importado entra como lido (cursor de todas as vagas no fim) e sem prazos de resposta — senão 843 mensagens velhas acordariam todo mundo e 28 viraria pendência vencida — custo: pendências reais antigas não aparecem
- Task 9: Ruling: só importa sprint ainda não terminada e se não houver outra aberta; exportação não escreve contadores (ids) — o manual recalcula pelo git — custo: nenhum relevante
- Task 9: Ruling: reserva bloqueada vira pronta + nota; concluída com veredito sim na mesma ponta vira aprovada; "nao" vira rejeitada — custo: veredito parcial vira aprovada

## Defeitos achados durante a execução

- Task 3: Ruling: deadlock achado (método do Core dentro de WithItems com uma conexão só): ItemTx.Sprint() e aviso no doc de WithItems — custo: nenhum
- Task 9: smoke: lockdir.Load no $LOCKS real do LyricCut leu 843 mensagens, 43 pontos, 16 reservas, 14 e2e, 128 ids, 0 erros
- Task 10: defeito achado pelo cenário: métricas contavam eventos de sincronização (sem "de") como nova integração — TestMetricasIgnoramEventoSemMudancaDeEstado RED→GREEN
