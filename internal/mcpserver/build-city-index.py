"""Build the embedded index from GeoNames cities1000.zip and tabular metadata.

Usage: python3 build-city-index.py cities1000.zip admin1CodesASCII.txt countryInfo.txt
Run from the directory containing this script.
"""

import csv
import gzip
import io
import sys
import zipfile


def read_table(path, column):
    with open(path, encoding="utf-8") as source:
        rows = csv.reader(source, delimiter="\t")
        return {row[0]: row[column] for row in rows if row and not row[0].startswith("#")}


regions = read_table(sys.argv[2], 1)
countries = read_table(sys.argv[3], 4)
with (
    zipfile.ZipFile(sys.argv[1]) as archive,
    archive.open("cities1000.txt") as raw,
    io.TextIOWrapper(raw, encoding="utf-8", newline="") as source,
    open("cities.tsv.gz", "wb") as destination,
    gzip.GzipFile(filename="", mode="wb", fileobj=destination, mtime=0) as compressed,
):
    for row in csv.reader(source, delimiter="\t", quoting=csv.QUOTE_NONE):
        code = row[8]
        region = regions.get(f"{code}.{row[10]}", "")
        names = dict.fromkeys([row[1], row[2], *filter(None, row[3].split(","))])
        fields = [row[0], row[1], code, countries.get(code, ""), region, row[4], row[5], row[14], ",".join(names)]
        compressed.write(("\t".join(fields) + "\n").encode("utf-8"))
