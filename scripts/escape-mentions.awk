# escape-mentions.awk: backtick every GitHub @-mention in a line of text.
#
# A release body is built from commit subjects, and GitHub links "@handle" there
# (listing the account as a contributor) when it starts the line or follows a
# non-word character, as in "*@pve". A handle inside a code span is never linked,
# so each one outside a span is wrapped in backticks; "root@pam" is left alone,
# and so is a URL's "/@", which GitHub links as the URL. A line with no mention
# prints as it came. Not handled, as no subject has had one: a handle right after
# a code span or a backslash. Run with LC_ALL=C: bytes, the same under any awk.

# closing returns where a run of exactly `run` backticks starts at or after
# `from`, or 0: by CommonMark, only such a run closes a code span.
function closing(s, from, run,    n, p, q) {
  n = length(s)
  p = from
  while (p <= n) {
    if (substr(s, p, 1) != "`") { p++; continue }
    q = p
    while (q <= n && substr(s, q, 1) == "`") q++
    if (q - p == run) return p
    p = q
  }
  return 0
}

{
  s = $0
  n = length(s)
  out = ""; prev = ""; tok = 1; wrapped = 0; i = 1
  while (i <= n) {
    c = substr(s, i, 1)
    if (c == "`") {
      j = i
      while (j <= n && substr(s, j, 1) == "`") j++
      k = closing(s, j, j - i)
      if (k) {
        # A code span: copied as it is, mentions and all.
        out = out substr(s, i, k + (j - i) - i)
        i = k + (j - i)
      } else {
        # Unmatched, so literal; escaped so an added backtick cannot pair with it.
        for (m = i; m < j; m++) out = out "\\`"
        i = j
      }
      prev = "`"
      continue
    }
    if (c == " " || c == "\t") tok = i + 1
    if (c == "@" && prev !~ /[A-Za-z0-9_]/ && substr(s, i + 1, 1) ~ /[A-Za-z0-9]/ && !inurl(substr(s, tok, i - tok))) {
      j = i + 1
      while (j <= n && substr(s, j, 1) ~ /[A-Za-z0-9-]/) j++
      if (substr(s, j, 1) == "/" && substr(s, j + 1, 1) ~ /[A-Za-z0-9]/) {
        j++
        while (j <= n && substr(s, j, 1) ~ /[A-Za-z0-9_-]/) j++
      }
      out = out "`" substr(s, i, j - i) "`"
      wrapped = 1
      prev = substr(s, j - 1, 1)
      i = j
      continue
    }
    out = out c
    prev = c
    i++
  }
  print (wrapped ? out : $0)
}

# inurl reports whether a word so far is a URL GitHub links whole.
function inurl(t) {
  return index(t, "://") > 0 || substr(t, 1, 4) == "www."
}
