A session is an endpoint only when `MASH_ENDPOINT`, `--endpoint` or `config.json` says which: `mash serve` and `mash hook`
no longer pick the one installed endpoint for themselves, so a session with none named is inert and `status` says so.
The one-installed-endpoint pick stays for the commands you type (`send`, `inbox`, `read`, `who`, `status` and the rest).
