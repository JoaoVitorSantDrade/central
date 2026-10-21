# M1a: decisões e pendências

Extraído do ledger de execução do plano [m1a-nucleo](../plans/m1a-nucleo.md). Revisões por tarefa feitas por subagentes (Opus/Sonnet); revisão final feita pelo controlador (sem subagentes, a pedido do humano).

## Decisões (Ruling)

- | T2..T10 | migração 3 editada incrementalmente (não publicada até o fim do M1a) | Ruling: aceitável porque nenhuma versão com a migração 3 sai da branch antes do merge — custo se errado: banco de dev local precisaria ser recriado (só testes usam) |
- Task 1: Ruling: CENTRAL_HOME absoluto repassado ao daemon (fora do brief) — necessário porque o daemon roda com cwd = pasta de dados — custo se errado: nenhum
- Task 2: Ruling: Enter com Config vazio mantém a configuração vigente; config salva inválida carrega padrões — protege chamadores da API e não impede abrir o projeto — custo se errado: um projeto pode rodar com padrões até o próximo entrar
- Task 3: Ruling: agente bloqueado em esperar longo (>= sumido_apos) apareceria sumido — Wait passa a renovar o batimento a cada 60 s enquanto espera (levado para a Task 4, que mexe em Wait/Tick) — custo se errado: um agente travado dentro de esperar nunca aparece sumido
- Task 3: Ruling: Touch (batimento) não grava evento — um evento por comando inundaria eventos; o batimento é estado efêmero e o sumiço gera evento próprio (agente.sumiu) — custo se errado: linha do tempo sem os batimentos (o painel usa o campo batimento)
- Task 4: Ruling: minor #1 (contagem de GERENTE inclui vagas encerradas/sumidas → aviso agente.sumiu se perde) reclassificado por efeito como Important e levado à Task 5 como item extra (uma linha + teste), revisado com ela — custo se errado: nenhum
- Task 6: Ruling: erro de conta no plano (4000+2000 > 5992); teste usa 1992 entra / 2000 e 2500 não — o algoritmo do brief é que manda — custo se errado: nenhum
- Task 7: Ruling: prioridade vazia → a menor configurada; WaitGrant ganha motivo `encerrada`; histórico só com tipo não vazio — preenchem lacunas do brief de forma conservadora — custo se errado: ajuste de contrato pequeno
- Task 8: Ruling: o medido soma o declarado das concessões ativadas depois da última amostra (o medidor ainda não as viu) — evita dois pedidos seguidos passarem na conta medida; fora do brief — custo se errado: admissão um pouco mais conservadora até a próxima leitura
- Task 8: Ruling: menores #2 (taskkill sem timeout trava o desligamento), #4 (buraco nas leituras gera sem_uso falso) e #7 (intervalo do medidor sem mínimo) reclassificados como Important pelo efeito e postos na rodada 1 — custo se errado: três pequenas mudanças a mais
- Task 9: Ruling: resumo de amostras da execução usa valor líquido (leitura − reservado, piso 0), igual ao pico da concessão — custo se errado: renomear campos
- Task 9: Ruling: soltar por id (menor #3) e travas considerando execução viva (#1) e SIGTERM (#4) entram na rodada 1 — o primeiro reclassificado como Important (pode soltar concessão alheia da mesma vaga) — custo: pouco código
- Task 10: Ruling: id.gerado com quem=CENTRAL perde a autoria — Core.NextID ganha o parâmetro vaga (diverge da assinatura do plano), levado à Task 11 como item extra — custo: uma assinatura
- Task 11: Ruling: medidor roda com cwd = raiz registrada do projeto (se existir), senão a pasta de dados, no daemon e no doctor — custo se errado: comando de medidor relativo a outra pasta precisa de caminho absoluto
- Task 11: Ruling: falha ao girar log não impede o daemon (#2) e backup logo ao subir (#4) reclassificados como Important pelo efeito — custo: pouco código
- Task 11: Ruling: backups de projeto em backups/projetos/ e sufixo -1/-2 no mesmo segundo (fora do brief) — evita colisão de nomes — custo: nenhum

## Menores pendentes

- Task 1: minor (deferred): checkDataDir aceita saude sem `dados` (daemon antigo) — remover a tolerância depois do M1a
- Task 1: minor (deferred): esperar repete 3x um erro de pasta de dados divergente que é permanente (~600 ms)
- Task 1: minor (deferred): service.stampLayout duplica core.timeLayout — exportar de core (levar para a Task 3)
- Task 1: minor (deferred): ensureDaemon calcula DataDir antes do health check
- Task 1: minor (deferred): hintEntrar vale para qualquer saída 3 em enviar/caixa/esperar — restringir se surgir outro 404 nesses caminhos
- Task 2: minor (deferred): validação aceita NaN/Inf em dimensoes/teto_medido
- Task 2: minor (deferred): ProjectConfig devolve cópia rasa (mapas/ponteiros compartilhados) — tratar como somente leitura
- Task 2: minor (deferred): CLI relê central.toml após FindProject (erro cru se sumir entre as leituras)
- Task 2: minor (deferred): faltam testes do mutex de Enter concorrente e do fallback de config salva que deixou de validar
- Task 3: minor (deferred): teste ignora erro de c.Touch (agents_test)
- Task 3: minor (deferred): TestSendEWaitRenovamBatimento usa sleep de 200 ms e pode não provar a renovação no retorno
- Task 3: minor (deferred): core.PadraoSumidoApos duplica config.PadraoSumidoApos
- Task 3: minor (deferred): SetStatus numa vaga encerrada: estado "" mantém encerrado; "ativo" revive sem evento agente.entrou
- Task 4: minor (deferred): FirstUnread/Pending abrem transação imediata a cada volta do Wait → disputa de trava de escrita com N agentes esperando
- Task 4: minor (deferred): Send com RespondeA não força Para == remetente original (só chamadas internas)
- Task 4: minor (deferred): SetLogf sem trava; documentar que deve ser chamado antes de RunTicker
- Task 4: minor (deferred): resposta.prazo de mensagem a TODOS vai para TODOS (literal do brief)
- Task 5: minor (deferred): vaga encerrada consegue tomar trava (que nasce morta) — recusar em Lock
- Task 5: minor (deferred): nomes de trava "." e ".." não podem ser soltos pela rota DELETE (mux limpa o caminho)
- Task 5: minor (deferred): CLI decide a dica pelo prefixo do texto da mensagem ("trava ") — frágil
- Task 5: minor (deferred): Lock idempotente ignora o motivo novo; faltam testes de dono_estado=encerrado
- Task 6: minor (deferred): cabeça bloqueada só pelo medido zera o backfill — documentar no Admit e fixar com teste (levar à Task 8)
- Task 6: minor (deferred): erro de ResourceSec.Spec descartado em setConfig sem log
- Task 6: minor (deferred): faltam testes de backfill atrás de cabeça exclusiva e de vários backfills na mesma chamada
- Task 6: minor (deferred): Declarado nil serializa como null
- Task 7: minor (deferred): ReleaseGrant/Leave só fazem Broadcast se algo foi ativado (quem espera um pedido encerrado só nota no próximo tick)
- Task 7: minor (deferred): ref inconsistente entre recurso.concedida (nome) e recurso.prazo/liberada (id)
- Task 7: minor (deferred): corpo de recurso.prazo mostra "10m0s"
- Task 7: minor (deferred): `recurso soltar` detecta "nada a soltar" pelo texto "não tem pedido"
- Task 7: minor (deferred): teste de expiração não fixa que A segue ativa entre prazo e prazo+10m
- Task 7: minor (deferred): validação antes da busca por chave (retentativa após mudança de config dá ErrInvalid)
- Task 8: minor (deferred): TestMedidorTimeout não prova que a árvore de processos morreu
- Task 8: minor (deferred): neto desanexado (start /b) sobrevive ao timeout — limite do kill por PID (Job Object resolveria)
- Task 8: minor (deferred): poda horária de amostras não usa índice (índice começa por recurso)
- Task 8: minor (deferred): limite de 1/min dos avisos de medidor marcado antes do commit, em memória
- Task 9: minor (deferred): Ctrl+C entre a concessão e o início do filho ainda inicia o filho e o mata
- Task 9: minor (deferred): no Windows o filho é morto na hora com taskkill, sem tempo de cortesia (fora do Windows, 10 s)
- Task 9: minor (deferred): resposta de início sem `execucao` vira eid "<nil>"
- Task 9: minor (deferred): batimento de 60 s e Ctrl+C com recurso sem teste automatizado (precisa de grupo de console)
- Task 9: minor (deferred): rota DELETE .../pedidos/{gid} sem teste de API próprio; mensagem ainda_na_fila mostra "1h0m0s"
- Task 10: minor (deferred): hintFato distingue por prefixo de texto ("fato "); evento fato.gravado sem o valor; teste com aritmética de rune
- Task 11: minor (deferred): doctor reprova `dados` inexistente numa máquina nova
- Task 11: minor (deferred): separação registro.db × projeto "registro" depende da convenção de pastas
- Task 11: minor (deferred): backup ao subir roda antes de o HTTP escutar — com bancos grandes pode passar dos 5 s do autostart
