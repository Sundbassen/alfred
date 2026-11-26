# ESPHome native API definition (vendored)

`api.proto` and `api_options.proto` define ESPHome's native API, the protocol
Alfred's hub uses to talk to its satellites (ESPHome speakers and
linux-voice-assistant). They are copied unchanged from
[aioesphomeapi](https://github.com/esphome/aioesphomeapi) **v45.3.1**
(`aioesphomeapi/api.proto`, `aioesphomeapi/api_options.proto`), the version
linux-voice-assistant v1.1.18 uses. aioesphomeapi is MIT-licensed; its license
is in [LICENSE](LICENSE).

Go code is generated into `backend/internal/esphome/esphomepb` with `make proto`
(see `backend/buf.gen.yaml`). To update: copy both files from a newer
aioesphomeapi tag, update the version above, run `make proto`, and check
`internal/esphome` still builds and passes its tests.
