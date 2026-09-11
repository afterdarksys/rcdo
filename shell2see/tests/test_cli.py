"""End-to-end checks using generated fixtures; requires Pillow and Poppler."""
import json
import io
import subprocess
import tempfile
import unittest
from pathlib import Path

from PIL import Image

ROOT = Path(__file__).resolve().parents[1]


def text_pdf(path):
    stream = b"BT /F1 18 Tf 40 100 Td (Shell patterns patterns) Tj ET"
    objects = [b"<< /Type /Catalog /Pages 2 0 R >>",
               b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
               b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
               b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
               b"<< /Length " + str(len(stream)).encode() + b" >>\nstream\n" + stream + b"\nendstream"]
    data = b"%PDF-1.4\n"
    offsets = [0]
    for number, obj in enumerate(objects, 1):
        offsets.append(len(data))
        data += f"{number} 0 obj\n".encode() + obj + b"\nendobj\n"
    xref = len(data)
    data += b"xref\n0 6\n0000000000 65535 f \n"
    data += b"".join(f"{offset:010d} 00000 n \n".encode() for offset in offsets[1:])
    data += f"trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n{xref}\n%%EOF\n".encode()
    path.write_bytes(data)


class CLITest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)

    def run_cli(self, name, *args, status=0):
        result = subprocess.run([str(ROOT / "bin" / name), *map(str, args)],
                                text=True, capture_output=True)
        self.assertEqual(result.returncode, status, result.stderr)
        return result.stdout

    def test_image_operations_and_overwrite_protection(self):
        source = self.directory / "image with spaces.png"
        Image.new("RGBA", (120, 60), (255, 0, 0, 128)).save(source)
        info = json.loads(self.run_cli("imgsee", source, "info"))
        self.assertEqual((info["width"], info["height"]), (120, 60))
        rows = self.run_cli("imgsee", source, "ascii", "--width", 20).splitlines()
        self.assertEqual(len(rows), 5)
        self.assertTrue(all(len(row) == 20 for row in rows))
        target = self.directory / "thumb.jpg"
        self.run_cli("imgsee", source, "thumb", target, "--size", 30)
        with Image.open(target) as image:
            self.assertEqual(image.size, (30, 15))
        before = target.read_bytes()
        self.run_cli("imgsee", source, "thumb", target, status=1)
        self.assertEqual(target.read_bytes(), before)
        self.run_cli("imgsee", source, "ascii", "--width", 0, status=2)

    def test_pdf_text_and_search(self):
        source = self.directory / "text with spaces.pdf"
        text_pdf(source)
        self.assertIn("Pages:", self.run_cli("pdfsee", source, "info"))
        self.assertIn("Shell patterns patterns", self.run_cli("pdfsee", source, "text"))
        self.assertIn("1:Shell", self.run_cli("pdfgrep", "shell", source))
        self.run_cli("pdfgrep", "absent", source, status=1)
        self.run_cli("pdfgrep", "[", source, status=2)
        self.run_cli("pdfgrep", "shell", self.directory / "missing.pdf", status=1)

    def test_embedded_images(self):
        source = self.directory / "scan.pdf"
        Image.new("RGB", (48, 24), "blue").save(source, "PDF")
        output = self.directory / "images"
        self.run_cli("pdfsee", source, "images", output)
        files = list(output.iterdir())
        self.assertTrue(files)
        with Image.open(files[0]) as image:
            self.assertEqual(image.size, (48, 24))
        self.run_cli("pdfsee", source, "images", output, status=1)

    def pipe_cli(self, name, *args, data, status=0):
        executable = ROOT / ("build" if name == "textsee" else "bin") / name
        result = subprocess.run([str(executable), *map(str, args)],
                                input=data, capture_output=True)
        self.assertEqual(result.returncode, status, result.stderr)
        return result.stdout

    def test_image_binary_pipeline(self):
        source = io.BytesIO()
        Image.new("RGB", (100, 50), "red").save(source, "PNG")
        thumbnail = self.pipe_cli("imgsee", "-", "thumb", "-", "--size", 20,
                                  data=source.getvalue())
        self.assertTrue(thumbnail.startswith(b"\x89PNG\r\n\x1a\n"))
        info = json.loads(self.pipe_cli("imgsee", "-", "info", data=thumbnail))
        self.assertEqual((info["width"], info["height"]), (20, 10))
        preview = self.pipe_cli("imgsee", "-", "ascii", "--width", 20, data=thumbnail)
        self.assertEqual(len(preview.splitlines()), 5)
        self.pipe_cli("imgsee", "-", "info", data=b"invalid", status=1)

    def test_pdf_stdin_pipeline(self):
        source = self.directory / "stdin.pdf"
        text_pdf(source)
        data = source.read_bytes()
        self.assertIn(b"Pages:", self.pipe_cli("pdfsee", "-", "info", data=data))
        text = self.pipe_cli("pdfsee", "-", "text", data=data)
        report = json.loads(self.pipe_cli("textsee", "-json", data=text))
        self.assertEqual(report["top"][0], {"text": "patterns", "count": 2})
        for arguments in [("shell",), ("shell", "-")]:
            self.assertIn(b"1:Shell", self.pipe_cli("pdfgrep", *arguments, data=data))
        self.pipe_cli("pdfsee", "-", "text", data=b"invalid", status=1)
        Image.new("RGB", (48, 24), "blue").save(source, "PDF")
        output = self.directory / "stdin-images"
        self.pipe_cli("pdfsee", "-", "images", output, data=source.read_bytes())
        self.assertTrue(list(output.iterdir()))

    def test_literal_text_and_stdin(self):
        report = json.loads(self.pipe_cli("textsee", "-json", "-text", "red red", data=b"ignored"))
        self.assertEqual(report["words"], 2)
        report = json.loads(self.pipe_cli("textsee", "-json", "-text", "red red", "-", data=b"blue"))
        self.assertEqual(report["words"], 3)
        report = json.loads(self.pipe_cli("textsee", "-json", "-text", "", data=b"ignored"))
        self.assertEqual(report["words"], 0)


if __name__ == "__main__":
    unittest.main()
