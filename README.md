# Ignit


![CI](https://github.com/rubiin/ignit/actions/workflows/ci.yml/badge.svg)
![AUR version](https://img.shields.io/aur/version/ignit-bin)
![Release](https://img.shields.io/github/v/release/rubiin/ignit)

<img src="./img.png" align="center" height="150"/>

A CLI app to quickly generate `.gitignore` files for your projects.

Pick your environment from a searchable list and ignit writes a ready-made
`.gitignore` in your current directory.

## Features

- **Instant startup** — the environment list is embedded in the binary, so the
  picker works fully offline. The network is only used when actually writing
  the `.gitignore`.
- **Fuzzy search** — start typing to filter the list.
- **Always fresh** — the embedded list can be regenerated from
  [toptal/gitignore.io](https://github.com/toptal/gitignore.io) with a single
  command.

## Install

Make sure [Go](https://go.dev) is installed, then run:

```sh
go install github.com/rubiin/ignit@latest
```

## Usage

```sh
ignit
```

Fire up your terminal, type `ignit`, and you get a nice select interface from
which you can choose what you want in your gitignore.

### Options

| Flag           | Description                                                              |
| -------------- | ------------------------------------------------------------------------ |
| `-update-list` | Re-fetch the template list from gitignore.io and regenerate the embedded list |
| `-v`, `--version` | Print the version                                                     |

Keyboard shortcuts: `↑`/`↓` to move, `/` to filter, `enter` to select,
`ctrl+c` to quit.

## Building from source

A [justfile](https://github.com/casey/just) is included:

```sh
just build   # build the binary
just test    # run tests
just lint    # golangci-lint
```

## Project layout

```
main.go              entry point: flags, program flow, .gitignore download
internal/picker/     fuzzy-filtering Bubble Tea select list
internal/envlist/    fetches and regenerates the environment list
internal/envs/       embedded environment snapshot (generated)
```

## License

[GPL-3.0](./LICENSE)

Made with ❤️ for opensource.
