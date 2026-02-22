# API Monitor (Go)

## Directory Layout (Updated)

```text
api_monitor_go/
├── internal/
│   └── app/                 # Go core application package
│       ├── run.go           # app.Start(...) startup entry
│       ├── admin.go         # admin auth/session/settings APIs
│       ├── db.go            # SQLite schema and data access
│       ├── handler.go       # monitor HTTP APIs
│       ├── monitor.go       # scheduler and probe execution
│       ├── proxy.go         # API proxy endpoints and key checks
│       └── sse.go           # SSE event bus and middleware
├── web/                     # embedded frontend pages/assets
├── data/                    # runtime database and logs
├── main.go                  # root binary entry (embed + app.Start)
├── Dockerfile
└── docker-compose.yml
```
`API Monitor (Go)` 鏄?`api_monitor` 鐨?Go 鐗堟湰瀹炵幇锛岀敤浜庢壒閲忕鐞?API 娓犻亾骞跺懆鏈熸€ф墽琛屾ā鍨嬫祴娲汇€?
浠撳簱鍦板潃锛歚https://github.com/ZhantaoLi/api_monitor_go`

## 鍔熻兘姒傝

- 娓犻亾绠＄悊锛氬鍒犳敼鏌ョ洰鏍囨笭閬擄紙`name + base_url + api_key`锛?- 瀹氭椂宸℃锛氬悗鍙版瘡鍒嗛挓鎵弿鍒版湡鐩爣骞惰Е鍙戞娴?- 骞跺彂妫€娴嬶細鐩爣鍐呭苟鍙戞娴嬫ā鍨嬶紝鐩爣闂村苟琛岃繍琛?- 缁撴灉钀藉簱锛歋QLite 淇濆瓨 `targets / runs / run_models`
- 瀹炴椂鎺ㄩ€侊細SSE 鎺ㄩ€?`run_completed`銆乣target_updated` 浜嬩欢
- Web 椤甸潰锛?  - 涓荤晫闈細`/`
  - 鏃ュ織椤甸潰锛歚/viewer.html?target_id=<id>`
  - 鍒嗘瀽椤甸潰锛歚/analysis.html?target_id=<id>`
- 娓犻亾鎺掑簭锛氫富鐣岄潰鎷栨嫿鎺掑簭锛屾寔涔呭寲鍒?`sort_order`
- 鏃ュ織鏌ヨ鏀寔鎸囧畾 `run_id`锛?  - `GET /api/targets/{id}/logs?run_id=<run_id>`
- API 浠ｇ悊锛圥roxy锛夛細
  - `GET /v1/models`
  - `POST /v1/chat/completions`
  - `POST /v1/messages`
  - `POST /v1/responses`
  - `POST /v1beta/models/{model}:generateContent`
  - `POST /v1beta/models/{model}:streamGenerateContent`
  - 浠ｇ悊鏂囨。椤碉細`GET /docs/proxy`

## 鎶€鏈爤

- 鍚庣锛欸o `net/http`锛圙o 1.22+ 璺敱璇硶锛?- 鏁版嵁搴擄細SQLite锛坄modernc.org/sqlite`锛?- 鍓嶇锛欻TML + Tailwind + Alpine.js + Chart.js
- 瀹炴椂锛歋erver-Sent Events (SSE)
- 閮ㄧ讲锛欴ocker Compose锛圠inux锛?
## 鐩綍缁撴瀯

```text
api_monitor_go/
鈹溾攢鈹€ data/                   # 杩愯鏈熸暟鎹洰褰曪紙榛樿蹇界暐锛?鈹?  鈹溾攢鈹€ logs/               # JSONL 鏄庣粏鏃ュ織鐩綍
鈹?  鈹斺攢鈹€ registry.db         # SQLite 涓绘暟鎹簱鏂囦欢
鈹溾攢鈹€ web/                    # 鍓嶇椤甸潰涓庨潤鎬佽祫婧?鈹?  鈹溾攢鈹€ assets/             # 椤甸潰鑴氭湰銆佹牱寮忎笌绗笁鏂硅祫婧?鈹?  鈹?  鈹溾攢鈹€ vendor/         # 绗笁鏂瑰墠绔簱涓庡瓧浣撴枃浠?鈹?  鈹?  鈹溾攢鈹€ analysis.js     # 鍒嗘瀽椤甸€昏緫
鈹?  鈹?  鈹溾攢鈹€ dashboard.js    # 涓荤晫闈㈤€昏緫锛堝惈鎷栨嫿鎺掑簭锛?鈹?  鈹?  鈹溾攢鈹€ log-viewer.js   # 鏃ュ織椤甸€昏緫
鈹?  鈹?  鈹溾攢鈹€ main.js         # 閫氱敤宸ュ叿涓庨壌鏉冮€昏緫
鈹?  鈹?  鈹溾攢鈹€ styles.css      # 鍏ㄥ眬鏍峰紡
鈹?  鈹?  鈹斺攢鈹€ tailwind.config.js # Tailwind 杩愯鏃堕厤缃?鈹?  鈹溾攢鈹€ analysis.html       # 鍒嗘瀽椤甸潰
鈹?  鈹溾攢鈹€ index.html          # 涓婚〉闈紙娓犻亾鎬昏锛?鈹?  鈹斺攢鈹€ log_viewer.html     # 鏃ュ織鏌ョ湅椤甸潰
鈹溾攢鈹€ .dockerignore           # Docker 鏋勫缓蹇界暐瑙勫垯
鈹溾攢鈹€ .gitignore              # Git 蹇界暐瑙勫垯
鈹溾攢鈹€ db.go                   # 鏁版嵁灞傦細琛ㄧ粨鏋勩€佽縼绉汇€丆RUD
鈹溾攢鈹€ docker-compose.yml      # Docker Compose 鍚姩閰嶇疆
鈹溾攢鈹€ Dockerfile              # 闀滃儚鏋勫缓瀹氫箟
鈹溾攢鈹€ go.mod                  # Go 妯″潡瀹氫箟
鈹溾攢鈹€ go.sum                  # Go 渚濊禆鏍￠獙閿佸畾
鈹溾攢鈹€ handler.go              # HTTP API 澶勭悊涓庡弬鏁版牎楠?鈹溾攢鈹€ LICENSE                 # MIT 璁稿彲璇?鈹溾攢鈹€ main.go                 # 绋嬪簭鍏ュ彛涓庤矾鐢辨敞鍐?鈹溾攢鈹€ monitor.go              # 璋冨害銆佹ā鍨嬫帰娴嬨€佺粨鏋滃啓鍏?鈹溾攢鈹€ README.md               # 椤圭洰璇存槑鏂囨。
鈹斺攢鈹€ sse.go                  # SSE 浜嬩欢鎬荤嚎涓庨壌鏉冧腑闂翠欢
```

## 鐜鍙橀噺

- `PORT`锛氭湇鍔＄鍙ｏ紝榛樿 `8081`
- `DATA_DIR`锛氭暟鎹洰褰曪紝榛樿 `data`
- `API_MONITOR_TOKEN`锛欰PI 閴存潈 Token锛涗负绌哄垯棣栨鍚姩闅忔満鐢熸垚骞舵墦鍗板埌缁堢
- `LOG_CLEANUP_ENABLED`锛氭棩蹇楁竻鐞嗗紑鍏筹紙榛樿寮€锛?- `LOG_MAX_SIZE_MB`锛氭棩蹇楃洰褰曟€诲ぇ灏忎笂闄愶紙榛樿 `500`锛?- `ADMIN_PANEL_PASSWORD`锛氬悗鍙扮鐞嗛潰鏉跨櫥褰曞瘑鐮侊紱涓虹┖鍒欓娆″惎鍔ㄩ殢鏈虹敓鎴愬苟鎵撳嵃鍒扮粓绔?- `DEFAULT_INTERVAL_MIN`锛氶粯璁ゆ笭閬撴娴嬮棿闅旓紙鍒嗛挓锛岄粯璁?`30`锛?- `PROXY_MASTER_TOKEN`锛氬彲鐩存帴璁块棶浠ｇ悊绔偣鐨勫叏灞€ token锛堝彲鍦ㄥ悗鍙伴潰鏉夸慨鏀癸級


## Linux Docker 杩愯

```bash
git clone https://github.com/ZhantaoLi/api_monitor_go
cd api_monitor_go
# 鍏堟妸 docker-compose.yml 閲岀殑闀滃儚 tagname 鏀规垚瀹為檯鐗堟湰锛堝 v1.0.0锛?docker compose pull
docker compose up -d
```

鑻?`docker-compose.yml` 涓湭鎻愪緵 `API_MONITOR_TOKEN` / `ADMIN_PANEL_PASSWORD`锛?棣栨鍚姩浼氳嚜鍔ㄩ殢鏈虹敓鎴愶紝骞跺彲閫氳繃 `docker compose logs -f` 鍦ㄧ粓绔湅鍒般€?
褰撳墠 `docker-compose.yml` 鏄犲皠涓?`8081:8081`锛岃闂細

- `http://127.0.0.1:8081/`

## 閴存潈璇存槑

- 闄?`GET /api/health` 鍜岄潤鎬侀〉闈㈠锛孉PI 闇€瑕?`Authorization: Bearer <token>`
- SSE 绔偣棰濆鏀寔 `?token=<token>`锛圗ventSource 鏃犳硶鑷畾涔?Header锛?- 閴存潈榛樿寮€鍚細鑻ユ湭閰嶇疆 `API_MONITOR_TOKEN`锛岄娆″惎鍔ㄤ細闅忔満鐢熸垚骞跺湪缁堢鎵撳嵃

## 绠＄悊闈㈡澘

- 鐧诲綍椤碉細`/admin/login`
- 绠＄悊椤碉細`/admin.html`
- 鏀寔閰嶇疆锛?  - `api_monitor_token`
  - `admin_panel_password`
  - `proxy_master_token`
  - 鏃ュ織鑷姩娓呯悊寮€鍏充笌鏈€澶т綋绉?- `ADMIN_PANEL_PASSWORD` 榛樿寮€鍚細鑻ユ湭閰嶇疆锛岄娆″惎鍔ㄤ細闅忔満鐢熸垚骞跺湪缁堢鎵撳嵃
- Proxy Keys 浠呭厑璁稿悗鍙扮鐞嗕細璇濇搷浣滐紙`/api/admin/login` 鐧诲綍鍚庯級


## 涓昏鎺ュ彛

- `GET /api/health`
- `GET /api/events`
- `GET /api/dashboard`
- `GET /api/targets`
- `GET /api/targets/{id}`
- `POST /api/targets`
- `PATCH /api/targets/{id}`
- `DELETE /api/targets/{id}`
- `POST /api/targets/{id}/run`
- `GET /api/proxy/keys`锛堢鐞嗗憳锛?- `POST /api/proxy/keys`锛堢鐞嗗憳锛?- `DELETE /api/proxy/keys/{id}`锛堢鐞嗗憳锛?- `GET /v1/models`锛堜唬鐞嗭級
- `POST /v1/chat/completions`锛堜唬鐞嗭級
- `POST /v1/messages`锛堜唬鐞嗭級
- `POST /v1/responses`锛堜唬鐞嗭級
- `POST /v1beta/models/{model}:generateContent`锛堜唬鐞嗭級
- `POST /v1beta/models/{model}:streamGenerateContent`锛堜唬鐞嗭級

## API 浠ｇ悊

閮ㄧ讲鍚庡彲浣滀负 API 浠ｇ悊浣跨敤銆傞粯璁ょ鍙ｄ负 `8081`锛堝彲閫氳繃 `PORT` 淇敼锛夈€?
璇锋眰鏃跺湪 `Authorization` 澶存惡甯︿唬鐞嗗瘑閽ワ細

```bash
curl http://localhost:8081/v1/models \
  -H "Authorization: Bearer sk-your-proxy-key"

curl http://localhost:8081/v1/chat/completions \
  -H "Authorization: Bearer sk-your-proxy-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hello"}]}'
```

`/v1/models` 鍙繑鍥炴渶杩戞娴嬩腑鈥滄垚鍔熼€氳繃鈥濈殑妯″瀷锛涜嫢涓虹┖锛岃鍏堝湪棣栭〉鎵ц涓€娆℃笭閬撴娴嬨€?
浠ｇ悊瀵嗛挜鏀寔鎸夋笭閬?妯″瀷缁村害闄愬埗璁块棶鏉冮檺锛屽彲閫氳繃绠＄悊鍛?API 绠＄悊锛?
- `GET /api/proxy/keys`
- `POST /api/proxy/keys`
- `DELETE /api/proxy/keys/{id}`

瀹屾暣鏂囨。璇疯闂細`/docs/proxy`

## 鏁版嵁璇存槑

- SQLite锛歚data/registry.db`
- JSONL 鏃ュ織锛歚data/logs/target_<id>_<timestamp>.jsonl`

`run_models` 鍏抽敭瀛楁锛?
- `protocol`, `model`, `success`, `duration`, `status_code`
- `error`, `content`, `route`, `endpoint`, `timestamp`, `run_id`

## 娉ㄦ剰浜嬮」

- `api_key` 褰撳墠鏄庢枃瀛樺偍鍦?SQLite锛岃缁撳悎纾佺洏鏉冮檺鍜岄儴缃查殧绂讳娇鐢ㄣ€?- 濡傛灉浣犲湪鍏綉閮ㄧ讲锛屽缓璁厤鍚堝弽鍚戜唬鐞嗐€両P 鐧藉悕鍗曟垨棰濆閴存潈銆?
## 璁稿彲璇?
鏈」鐩噰鐢?MIT License锛岃 `LICENSE`銆?
