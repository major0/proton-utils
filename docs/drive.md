# Drive Commands

The `proton drive` command group manages files and directories in
Proton Drive.

## Path Format

All remote paths use the `proton://` prefix. The first component is the
share name (typically "My files" for the main share).

```
proton://My files/Documents/report.pdf
proton://My files/Projects/
```

> **Note:** The Photos share (`proton://Photos/`) supports uploading
> images to the root (`proton drive cp photo.jpg proton://Photos/`) but
> does not support writing into album subdirectories. Albums are a
> Photos-specific construct — from the Drive perspective they appear as
> read-only directories (`dr-xr-xr-x`). Album management (create,
> add/remove photos) will be handled by a future `proton photos`
> subcommand.

## Listing Files

```sh
proton drive ls [options] [<path> ...]
```

Options:
- `-l` — long format (permissions, size, date, name)
- `-a` — show all entries including trashed
- `-A` — show all entries except `.` and `..`
- `-F` — append type indicators (/ for dirs)
- `-R` — recursive listing
- `-1` — one entry per line
- `-x` — list entries by lines instead of columns
- `-C` — columnar output
- `-S` — sort by file size, largest first
- `-t` — sort by modification time, newest first
- `-U` — do not sort; list in directory order
- `-r` — reverse sort order
- `-i` — show link IDs
- `--color` — colorize output (auto, always, never)
- `--trash` — show only trashed entries
- `--human-readable` — print sizes in human-readable format
- `--full-time` — like `-l --time-style=full-iso`
- `--time-style` — time format: full-iso, long-iso, iso
- `--sort` — sort by: name, size, time, none
- `--format` — output format: long, single-column, across, columns

## Finding Files

Unix `find`-compatible search with single-hyphen flags:

```sh
proton drive find [<path>] [options]
```

Options:
- `-type f|d` — filter by type (file or directory)
- `-name <pattern>` — match name (glob)
- `-iname <pattern>` — case-insensitive name match
- `-maxdepth <n>` — limit traversal depth
- `-minsize <n>` — minimum file size in bytes
- `-maxsize <n>` — maximum file size in bytes
- `-mtime <n>` — modified time in days (negative=within N days, positive=older)
- `-newer <date>` — match files newer than ISO date (YYYY-MM-DD)
- `-print0` — separate output with NUL instead of newline
- `-depth` — process directory contents before the directory itself
- `-trashed` — include trashed items in results

Examples:

```sh
proton drive find proton://My\ files/ -type f -iname '*.pdf'
proton drive find -maxdepth 2 -type d -name 'src'
```

## Copying Files

```sh
proton drive cp [options] <source> [<source> ...] <dest>
```

Copies between local filesystem and Proton Drive in either direction.

Options:
- `-r` / `--recursive` — copy directories recursively
- `-f` / `--force` — overwrite existing files
- `--backup` — rename existing destination to `<name>~`
- `--remove-destination` — delete destination before copy
- `--progress` — show transfer progress
- `-v` / `--verbose` — print each operation

Examples:

```sh
# Upload
proton drive cp ./report.pdf proton://My\ files/Documents/

# Download
proton drive cp proton://My\ files/photo.jpg ./downloads/

# Recursive upload
proton drive cp -r ./project/ proton://My\ files/projects/
```

## Moving and Renaming

```sh
proton drive mv [options] <source> [<source> ...] <dest>
```

Moves or renames files and directories within Proton Drive.
Cross-volume moves are not supported.

Options:
- `-v` / `--verbose` — print each move operation

Examples:

```sh
# Rename
proton drive mv proton://My\ files/old-name proton://My\ files/new-name

# Move into directory
proton drive mv proton://src1 proton://src2 proton://dest-dir/
```

## Creating Directories

```sh
proton drive mkdir [options] <path> [<path> ...]
```

Options:
- `-p` — create parent directories as needed (mkdir -p)

## Removing Files

```sh
proton drive rm [options] <path> [<path> ...]
```

Moves files to trash by default.

Options:
- `-r` / `--recursive` — remove directories and contents
- `--permanent` — permanently delete (skip trash)

```sh
proton drive rmdir <path> [<path> ...]   # remove empty directories
proton drive empty-trash                  # permanently delete all trash
```

## Volume Usage

```sh
proton drive df
```

Shows disk usage per volume in df-style output.

## Share Management

```sh
proton drive share list          # list all shares
proton drive share add <path>    # create share from existing link
proton drive share del <name>    # delete a share
proton drive share show <name>   # show detailed share information
proton drive share rename <name> <new-name>  # rename a share
proton drive share invite <name> <email>     # invite a user to a share
proton drive share revoke <name> <email>     # revoke access to a share
proton drive share url enable <name>         # enable public URL
proton drive share url disable <name>        # disable public URL
proton drive share url password <name>       # manage URL password
```
