Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# Glossary

The words mash uses, and what each one means.

| Word | From | Meaning |
| --- | --- | --- |
| `account` | NATS | The NATS unit users belong to, and which signs them. |
| `address` | mash | owner.group.name, for example acme.demo.bob. |
| `agent, prompt, status, heartbeat` | SAP | An endpoint as SAP sees it, and SAP's live operations. |
| `creds file` | NATS | One text file, in the style of a PEM file, holding a user JWT and its seed. |
| `endpoint` | mash | One mailbox on the mesh, used by one agent, tool or person. |
| `group` | mash | The second part: endpoints an owner keeps together. |
| `inbox` | mash | The messages an endpoint has received. |
| `join file` | mash | One text file that lets a machine use an endpoint: server address, certificate authority, creds. |
| `kind, urgent` | mash | What sort of message it is, and whether it should interrupt. |
| `mesh` | mash | The whole: every endpoint and all their mail. |
| `message` | mash | One item of mail. |
| `name` | mash | The third part: this one endpoint. |
| `online / offline` | mash | Whether something is holding the endpoint open right now. |
| `owner` | mash | The first part of an address: a namespace. In 0.5.6 a mesh has one owner, named at mashctl init. Several owners per mesh are planned for 0.6.0. |
| `public key` | NATS | The half that can be shared. |
| `seed` | NATS | The private half of a key pair. |
| `sender identity` | SAP | SAP's optional signature on a message, which mash requires. |
| `server` | NATS | A NATS server: a way in to the mesh. |
| `user JWT` | NATS | A statement signed by the account that a public key is a given user. |
| `verified` | mash | Proven to come from the address it names. |
