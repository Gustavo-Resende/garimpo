# CLAUDE.md — Garimpo

## O que é o Garimpo

Garimpo é um sistema automatizado de divulgação de ofertas no WhatsApp. Ele extrai produtos em destaque da API de afiliados da Shopee, gera mensagens de divulgação com IA (Gemini 2.5 Flash) e as envia automaticamente para um grupo de WhatsApp via Evolution API.

O sistema roda como um único processo Go com dois loops internos independentes: um para extração de produtos e outro para postagem.

## Stack

- **Linguagem:** Go
- **Banco de dados:** SQLite (fila de produtos)
- **API de produtos:** Shopee Affiliate API (GraphQL)
- **IA:** Google Gemini 2.5 Flash (geração de mensagens)
- **WhatsApp:** Evolution API
- **Infra:** VPS Hostinger, processo gerenciado por systemd

## Estrutura de pastas

```
garimpo/
├── cmd/
│   └── garimpo/
│       └── main.go            # ponto de entrada, inicia goroutines
├── internal/
│   ├── shopee/
│   │   └── client.go          # cliente GraphQL, autenticação SHA256
│   ├── gemini/
│   │   └── client.go          # cliente Gemini, gera mensagem
│   ├── evolution/
│   │   └── client.go          # cliente Evolution API, envia WPP
│   ├── queue/
│   │   └── queue.go           # operações SQLite (enqueue, dequeue, status)
│   ├── scheduler/
│   │   └── scheduler.go       # verifica janela horária 07:00-23:00
│   └── worker/
│       ├── extractor.go       # orquestra: shopee → queue
│       └── poster.go          # orquestra: queue → gemini → evolution
├── garimpo.db                 # gerado em runtime pelo sistema
├── .env
├── .env.example
├── CLAUDE.md
├── go.mod
└── go.sum
```

## Como o sistema funciona

### Loop de extração (a cada 4h)

1. Chama o endpoint `productOfferV2` da API Shopee
2. Filtra produtos com `commissionRate >= MIN_COMMISSION` (configurável via `.env`)
3. Salva os produtos na tabela `queue` com status `pending`
4. Ignora produtos que já estão na fila com status `pending` (deduplicação por `offer_link`)

### Loop de postagem (a cada 12min)

1. Verifica se o horário atual está dentro da janela permitida (07:00-23:00)
2. Se estiver fora da janela, pula a execução sem fazer nada
3. Busca o próximo produto com status `pending` (mais antigo primeiro)
4. Envia os dados do produto para o Gemini gerar a mensagem
5. Posta a mensagem no grupo WPP via Evolution API
6. Atualiza o status do produto para `sent` com `sent_at = now()`
7. Em caso de erro, marca como `failed` e segue para o próximo na próxima rodada

### Janela horária

- **07:00 - 23:00:** funcionamento normal, posta a cada 12min
- **23:00 - 07:00:** pausa total, nenhum post é enviado

## Schema do banco de dados

```sql
CREATE TABLE IF NOT EXISTS queue (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    title        TEXT NOT NULL,
    price        REAL NOT NULL,
    discount     INTEGER,
    commission   REAL NOT NULL,
    image_url    TEXT,
    offer_link   TEXT NOT NULL UNIQUE,
    source       TEXT NOT NULL DEFAULT 'shopee',
    status       TEXT NOT NULL DEFAULT 'pending',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    sent_at      DATETIME
);
```

Campos:
- `status`: `pending` | `sent` | `failed`
- `source`: `shopee` (futuramente: `amazon`, `mercadolivre`)
- `offer_link` tem constraint `UNIQUE` para evitar duplicatas

## API Shopee (GraphQL)

**URL base:** `https://open-api.affiliate.shopee.com.br/graphql`
**Método:** sempre POST

### Autenticação

Header `Authorization` obrigatório em todas as requisições:

```
Authorization: SHA256 Credential={APP_ID}, Timestamp={TIMESTAMP}, Signature={SIGNATURE}
```

Assinatura calculada assim:

```
SIGNATURE = SHA256(APP_ID + TIMESTAMP + PAYLOAD + SECRET)
```

Onde `TIMESTAMP` é Unix timestamp em segundos (não milissegundos).

### Query principal (productOfferV2)

```graphql
{
  productOfferV2(
    listType: 1,
    sortType: 5,
    page: 1,
    limit: 20
  ) {
    nodes {
      itemId
      productName
      productLink
      offerLink
      imageUrl
      priceMin
      priceMax
      priceDiscountRate
      sales
      ratingStar
      commissionRate
      commission
      shopName
    }
    pageInfo {
      page
      limit
      hasNextPage
    }
  }
}
```

`listType: 1, sortType: 5` = lista de ofertas ordenada por comissão.

## Geração de mensagem (Gemini)

O cliente Gemini recebe os dados do produto e retorna uma mensagem pronta para o WhatsApp. O prompt deve instruir o modelo a:

- Usar linguagem animada e informal (português brasileiro)
- Usar emojis relevantes
- Destacar o desconto e a urgência
- Manter a mensagem curta (máx 5 linhas)
- Incluir o link de afiliado no final
- Não inventar informações que não foram passadas

Exemplo de saída esperada:

```
🔥 *OFERTA IMPERDÍVEL!*

📦 Panela de Pressão Elétrica 6L
💰 *R$ 349,89* — 30% OFF
⭐ 55 vendas confirmadas

👇 Corre antes que acabe:
https://shope.ee/abc123
```

## Evolution API (WhatsApp)

O cliente Evolution API envia mensagens para um grupo fixo configurado via `.env`.

Endpoint de envio:
```
POST /message/sendText/{INSTANCE_NAME}
```

Body:
```json
{
  "number": "GROUP_JID",
  "text": "mensagem gerada pelo Gemini"
}
```

## Variáveis de ambiente (.env)

```env
# Shopee
SHOPEE_APP_ID=
SHOPEE_SECRET=

# Gemini
GEMINI_API_KEY=

# Evolution API
EVOLUTION_API_URL=
EVOLUTION_API_KEY=
EVOLUTION_INSTANCE_NAME=
EVOLUTION_GROUP_JID=

# Configurações do sistema
MIN_COMMISSION=0.08
EXTRACTION_INTERVAL_HOURS=4
POSTING_INTERVAL_MINUTES=12
POSTING_START_HOUR=7
POSTING_END_HOUR=23

# Banco de dados
DB_PATH=./garimpo.db
```

## Regras de desenvolvimento

### Estilo Go
- Sem frameworks externos desnecessários — `net/http` padrão para HTTP
- Sem Clean Architecture, sem camadas desnecessárias
- Erros tratados explicitamente, nunca ignorados com `_`
- Logs simples com `log/slog` (padrão do Go 1.21+)
- Variáveis de ambiente carregadas com `os.Getenv`, sem biblioteca extra se possível

### Banco de dados
- Usar `database/sql` com driver `github.com/mattn/go-sqlite3`
- Abrir uma única conexão e passar via dependência
- Toda query usa prepared statements

### HTTP
- Timeout em todos os clientes HTTP (mínimo 10s)
- Retry simples (3 tentativas) nos clientes de API externos
- Nunca logar secrets ou API keys

### Concorrência
- Os dois workers rodam em goroutines separadas
- Cada worker tem seu próprio ticker
- Erros dentro dos workers são logados mas não derrubam o processo

## O que NÃO fazer

- Não adicionar camadas de abstração sem necessidade real
- Não usar ORMs — queries SQL diretas
- Não adicionar dependências externas sem discutir antes
- Não postar fora da janela 07:00-23:00
- Não repostar produtos que já estão na fila como `pending` ou `sent`
- Não expor API keys em logs

## Roadmap futuro (não implementar agora)

- Integração com Amazon Afiliados
- Integração com Mercado Livre
- Filtros por categoria de produto
- Suporte a múltiplos grupos de WPP
- Dashboard de métricas (cliques, conversões)
- Lista de transmissão quando grupo lotar
