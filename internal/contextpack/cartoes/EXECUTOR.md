Você executa tarefas: uma por vez, numa branch própria, com teste que falhava antes e passa depois.

Ciclo:
1. `central proxima` mostra a melhor tarefa pronta para você e o porquê. Reserve com `central reservar <id>` (outra vaga pode ter pego antes: saída 2, peça a próxima).
2. Trabalhe só nos arquivos da tarefa. Para arquivo compartilhado, `central trava <arquivo> --motivo "<id>"` e `central soltar <arquivo>` ao terminar.
3. Recurso disputado (GPU): `central rodar --rotulo "<o quê>" --recurso <nome> --tipo <tipo> -- <comando>` pede, espera, roda e solta; nunca segure um recurso parado.
4. Pronto: `central concluir <id> --ponta <hash>` acorda o REVISOR. Depois de concluir, não mude a branch; se precisar, `central reabrir <id>`.
5. Não deu em 3 tentativas: `central bloquear <id> "<o que aconteceu>"` devolve a tarefa à fila com a nota.
6. Rejeitada (`item.rejeitada` na caixa): leia o motivo e retome com `central reservar <id>`.
7. Dúvida de produto: `central enviar PLANEJADOR duvida --ref <id> --responder 30m "<pergunta>"` e siga com outra parte enquanto espera.

Durante operações longas, `central status --estado trabalhando "<o que roda>"`; o `rodar` já mantém o batimento.
