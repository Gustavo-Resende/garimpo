# Garimpo

Bot automatizado de curadoria e postagem de ofertas da Shopee no WhatsApp. O sistema extrai produtos via API de afiliados da Shopee, apresenta cada oferta para aprovação humana via bot do Telegram e posta os aprovados num grupo de WhatsApp.

## Como funciona

```
Shopee API → fila (pending_review) → curadoria no Telegram → fila (pending) → WhatsApp
```

**1. Extração** — A cada 4 horas (configurável), o sistema consulta a API de afiliados da Shopee e filtra produtos por comissão mínima, volume de vendas e avaliação. Os produtos que passam nos filtros entram na fila com status `pending_review` e são enviados ao grupo de curadoria no Telegram.

**2. Curadoria** — O bot do Telegram apresenta cada produto com imagem, mensagem formatada e três botões: aprovar, recusar ou trocar imagem. A curadoria é 100% humana — nenhuma IA avalia se um produto deve ser postado.

**3. Postagem** — A cada intervalo aleatório entre 4 e 12 minutos (configurável), dentro da janela 07:00–23:00, o sistema pega o próximo produto aprovado, gera uma mensagem de venda via Gemini e posta no grupo do WhatsApp via Evolution API.

## Stack

| Componente | Tecnologia |
|---|---|
| Linguagem | Go |
| Banco de dados | SQLite (`modernc.org/sqlite`) |
| API de produtos | Shopee Affiliate API (GraphQL) |
| Geração de mensagem | Google Gemini 2.5 Flash |
| Curadoria | Bot do Telegram (long polling) |
| WhatsApp | Evolution API |
| Deploy | Railway |

## Variáveis de ambiente

Copie `.env.example` para `.env` e preencha os valores.

### Obrigatórias

| Variável | Descrição |
|---|---|
| `SHOPEE_APP_ID` | App ID da conta de afiliados da Shopee |
| `SHOPEE_SECRET` | Secret da conta de afiliados da Shopee |
| `GEMINI_API_KEY` | Chave da API do Google Gemini |
| `EVOLUTION_API_URL` | URL base da instância Evolution API |
| `EVOLUTION_API_KEY` | Chave de autenticação da Evolution API |
| `EVOLUTION_INSTANCE_NAME` | Nome da instância WhatsApp na Evolution |
| `EVOLUTION_GROUP_JID` | JID do grupo de WhatsApp onde as ofertas são postadas |
| `TELEGRAM_BOT_TOKEN` | Token do bot do Telegram (via BotFather) |
| `TELEGRAM_CHAT_ID` | ID do grupo de curadoria no Telegram |
| `DB_PATH` | Caminho do arquivo SQLite (ex: `./garimpo.db`) |

### Filtros de produto

| Variável | Padrão | Descrição |
|---|---|---|
| `MIN_COMMISSION` | `0.08` | Comissão mínima aceitável (8%) |
| `MAX_COMMISSION` | `0.40` | Comissão máxima aceitável (40%) |
| `MIN_SALES` | `500` | Número mínimo de vendas do produto |
| `MIN_RATING` | `4.0` | Avaliação mínima do produto |
| `SHOPEE_PRODUCT_LIMIT` | `50` | Produtos por página na API da Shopee |
| `TARGET_QUEUE_SIZE` | `30` | Quantidade alvo de produtos na fila de curadoria |
| `SEARCH_KEYWORDS` | — | Keywords de busca separadas por vírgula (opcional) |
| `MAX_PER_KEYWORD` | `5` | Máximo de produtos por keyword |

### Agendamento e postagem

| Variável | Padrão | Descrição |
|---|---|---|
| `EXTRACTION_INTERVAL_HOURS` | `4` | Intervalo entre extrações da Shopee (horas) |
| `POSTING_MIN_INTERVAL_MINUTES` | `4` | Intervalo mínimo entre postagens (minutos) |
| `POSTING_MAX_INTERVAL_MINUTES` | `12` | Intervalo máximo entre postagens (minutos) |
| `POSTING_START_HOUR` | `7` | Hora de início das postagens |
| `POSTING_END_HOUR` | `23` | Hora de encerramento das postagens |
| `LOW_QUEUE_THRESHOLD` | `5` | Alerta de fila baixa (notificação via Telegram) |

### Opcionais

| Variável | Descrição |
|---|---|
| `GOOGLE_SHEETS_CREDENTIALS` | JSON de credenciais de serviço do Google (para integração com Sheets) |
| `GOOGLE_SHEETS_ID` | ID da planilha Google Sheets |
| `N8N_WEBHOOK_URL` | URL de webhook do n8n para automações externas |

## Como rodar localmente

**Pré-requisitos:** Go 1.21+, Evolution API rodando e acessível.

```bash
# Clonar o repositório
git clone https://github.com/Gustavo-Resende/garimpo.git
cd garimpo

# Configurar variáveis de ambiente
cp .env.example .env
# edite .env com seus valores

# Baixar dependências
go mod download

# Rodar
go run ./cmd/garimpo
```

O banco de dados SQLite é criado automaticamente no caminho definido em `DB_PATH`.

## Deploy

O projeto é deployado no **Railway** como um único serviço Go. As variáveis de ambiente são configuradas diretamente no painel do Railway. Não há necessidade de configuração adicional — o binário lê todas as configurações via env vars na inicialização.

```bash
# Build para produção
go build -o garimpo ./cmd/garimpo
```

## Estrutura do projeto

```
garimpo/
├── cmd/garimpo/main.go          # entrypoint, inicialização dos workers
├── internal/
│   ├── shopee/client.go         # cliente da API de afiliados da Shopee
│   ├── gemini/client.go         # geração de mensagem via Gemini
│   ├── evolution/client.go      # cliente da Evolution API (WhatsApp)
│   ├── telegram/
│   │   ├── client.go            # envio de mensagens para o grupo de curadoria
│   │   └── handler.go           # polling e handlers dos botões inline
│   ├── queue/queue.go           # acesso ao banco SQLite (fila de produtos)
│   ├── scheduler/scheduler.go   # agendamento dos loops
│   └── worker/
│       ├── extractor.go         # loop: Shopee → fila → Telegram
│       └── poster.go            # loop: fila → Gemini → WhatsApp
├── .env.example
├── go.mod
└── go.sum
```
