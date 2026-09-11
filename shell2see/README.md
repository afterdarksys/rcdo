# shell2see

Small Go, Python, and Bash utilities demonstrating how a shell can help you see
patterns in text, preview images, and inspect the contents of PDFs.

| Utility | Language | What it reveals |
| --- | --- | --- |
| `textsee` | Go, standard library | Word frequencies as terminal bars or JSON |
| `imgsee` | Python + Pillow | Image metadata, ASCII previews, thumbnails |
| `pdfsee` | Python + Poppler | PDF metadata, text, and embedded image files |
| `pdfgrep` | Bash | Matching lines in one or more PDFs |

## Setup

Use macOS or Linux with Go 1.22+, Python 3.9+, Bash, and Make.

```sh
python3 -m venv .venv
. .venv/bin/activate
python3 -m pip install -r requirements.txt
# macOS:
brew install poppler
# Debian/Ubuntu alternative: sudo apt install poppler-utils
make build
export PATH="$PWD/bin:$PWD/build:$PATH"
```

`textsee` needs only Go. `imgsee` needs Pillow. The PDF utilities need Poppler;
`pdfsee` uses only Python's standard library. Run commands below from this folder
after setup. Each command supports `--help`.

## See patterns in text

```sh
textsee -top 5 examples/sample.txt
textsee -text 'red blue red green red'
cat examples/sample.txt | textsee -json
printf 'red blue red green red\n' | textsee
```

The last command prints:

```text
Words: 5  Unique: 3
     3  red                  ##############################
     1  blue                 ##########
     1  green                ##########
```

Words are runs of Unicode letters or numbers, lowercased; punctuation splits
words. Ties sort alphabetically. Multiple files are combined, and `-` reads stdin.
`-text TEXT` supplies literal text instead of a filename. With `-text`, stdin is
read only when you explicitly add `-`; literal text is counted before file inputs.
Place flags before filenames. Input is held in memory, so this is intended for
small to medium text collections.

## See images in a text terminal

Generate a sample gradient, then inspect it:

```sh
mkdir -p out
python3 - <<'PY'
from PIL import Image
image = Image.new('RGB', (256, 128))
image.putdata([(x, y * 2, 255 - x) for y in range(128) for x in range(256)])
image.save('out/gradient.png')
PY
imgsee out/gradient.png info
imgsee out/gradient.png ascii --width 60
imgsee out/gradient.png ascii --width 60 --invert
imgsee out/gradient.png thumb out/small.jpg --size 96
```

ASCII previews compensate approximately for terminal character proportions.
Try `--invert` for the opposite brightness mapping. EXIF orientation is applied;
animated inputs use their first frame. Thumbnails preserve aspect ratio and
refuse to overwrite an existing file. Transparent pixels use a white background
for ASCII previews and JPEG output.

## See inside PDFs

Replace `document.pdf` with a PDF you have:

```sh
pdfsee document.pdf info
pdfsee document.pdf text > out/document.txt
pdfsee document.pdf text | textsee -top 15
pdfsee document.pdf images out/extracted-images
pdfgrep 'invoice|total|balance' document.pdf
pdfgrep 'error|warning' first.pdf 'second document.pdf'
```

`text` writes UTF-8 text with approximate layout preservation. `images` extracts
embedded images in their native formats into a **new directory**, which must not
already exist. It does not render whole pages. Some extracted formats or masks
may need conversion before Pillow can open them. A PDF without embedded images
produces an empty directory.

Scanned PDFs may have no text layer: these utilities do not perform OCR. PDF text
order and spacing depend on the source document. Password-protected PDFs may fail.
If image extraction fails, partial output remains available for inspection.

The shell example uses a temporary file to distinguish extraction errors from
search misses, then runs `grep -niE`. Search uses case-insensitive extended regular
expressions and reports extracted-text line numbers, not PDF page numbers.
Omit the PDF arguments or use `-` to search PDF bytes arriving on stdin.
Exit status is 0 for any match, 1 for no matches or an extraction failure (with a
diagnostic for failures), and 2 for invalid arguments or regular expressions.

## Connect commands with pipes

All four utilities accept stdin and produce output suitable for pipes. File
arguments name files; literal text uses `textsee -text`. Image and PDF stdin
contain the actual binary file contents, not a filename or base64 string.

```sh
# Literal arguments, stdin, or both:
textsee -text 'hello shell hello'
printf 'hello shell hello\n' | textsee -json
printf 'blue green\n' | textsee -text 'red red' -

# Image bytes -> resized PNG bytes -> terminal preview:
cat out/gradient.png | imgsee - ascii --width 60
imgsee out/gradient.png thumb - --size 96 | imgsee - ascii --width 40
cat out/gradient.png | imgsee - thumb - --size 96 > out/piped-thumb.png

# PDF bytes -> text -> word frequencies:
cat document.pdf | pdfsee - text | textsee -top 10
cat document.pdf | pdfsee - info
cat document.pdf | pdfsee - images out/piped-images
cat document.pdf | pdfgrep 'invoice|total'
```

`imgsee ... thumb -` always emits PNG bytes with no status text mixed in.
Other image commands emit text or JSON. PDF text and metadata go to stdout;
embedded-image extraction still needs an output directory because it can produce
multiple files. Errors go to stderr. In Bash, use `set -o pipefail` when you want
a pipeline to fail if an earlier command fails.

Image stdin is buffered in memory to allow seeking; accepting a pipe does not
mean processing incrementally. Use `./-` to refer to an actual file named `-`.

## Check the examples

```sh
make test
```

Tests generate their own images and PDFs in temporary directories. They cover
Unicode word counting, ordering, empty text, previews, thumbnail dimensions,
overwrite protection, real PDF extraction, and shell search behavior.
