# stampla

> **Early development.** This is the ground-up Go implementation of
> Stampla. It has not shipped a release yet. The previous Python
> implementation lives on as [stampla-python](https://github.com/stampla/stampla-python)
> (maintenance only).

Stampla stamps every photo and video with its own identity: when it
was captured and what it contains.

```
20260703_150727_9b677b64.nef
└──────┬──────┘ └──┬───┘
  capture time    hash
```

The name is derived purely from the file itself — capture time plus a
slice of the image-data hash — so it is stable, sortable, and
reproducible from the file at any time. The filename is the identity
*and* the checksum; verification needs no database.

## Commands

```
stampla cp <inputs...> <dest>    copy files into an archive under their identity names
stampla mv <inputs...> <dest>    move files into place; also heals an archive in place
stampla verify <src> <dest>      is everything in src accounted for in dest? exit 0 = yes
stampla verify <dest>            check an archive: names, placement, integrity
```

One engine underneath: converge files onto their identities under a
destination root. `verify` never mutates. `cp` and `mv` act, preview
with `-n`, and ask before anything unusual (`-y` skips the questions).
`mv` deletes a source file only after the copy is re-hashed and
verified at its destination.

Every archive declares its directory layout in a one-line `.stampla`
file that travels with the photos. Every mutation appends to a
plain-text receipt beside it. There is no catalog, no database, and no
lock-in: delete the tool and your files remain exactly what they are.

See [DESIGN.md](DESIGN.md) for the full design.

## Requirements

- [ExifTool](https://exiftool.org/) on `PATH` (the only external
  dependency).

## Status

Pre-release. The command surface above is implemented and tested but
has not been tagged; interfaces may still change until v0.1.0.

## License

GPL-3.0-or-later. See [LICENSE](LICENSE).
