# boot.py: runs once at power-up, before USB enumerates.
# Enables the second USB CDC serial port that Tally will use for status
# frames. Harmless until Tally exists. Takes effect after a hard reset
# (press the reset button or replug), not on a code.py auto-reload.
import usb_cdc

usb_cdc.enable(console=True, data=True)
