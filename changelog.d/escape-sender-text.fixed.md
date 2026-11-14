The live prompt text and the `mash who` output now show control characters as visible `\xNN` escapes instead of passing
them through, and `mash who` cuts each directory field at 256 characters. A live prompt is at most 32768 bytes: a longer
one is refused with an error to the caller (`prompt_too_large`, send the longer text as mail) instead of being cut, so
the session never answers a half request. Newlines and tabs in a prompt are kept.
