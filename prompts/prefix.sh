# Runs once whenever a new shell session starts (also after /new, /reload or
# a lost session). Define functions and variables here; they persist.
if [ -f README.md ]; then cat README.md; fi
