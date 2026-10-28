# Runs once whenever a new shell session starts (also after /new, /reload or
# a lost session). Define functions and variables here; they persist.
if [ -f AGENTS.md ]; then cat AGENTS.md; fi
