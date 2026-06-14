# util.R — geometry helpers sourced by main.R.

# Area of a circle given its radius.
area <- function(r) {
  pi * r^2
}

# Helper that doubles its argument; used by area's caller.
twice <- function(x) {
  x * 2
}
