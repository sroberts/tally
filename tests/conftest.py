# Firmware modules import by bare name, as on CIRCUITPY. Append rather than
# prepend: firmware/code.py would shadow the standard library's code module.
import pathlib
import sys

sys.path.append(str(pathlib.Path(__file__).resolve().parent.parent / "firmware"))
