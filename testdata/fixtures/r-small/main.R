# main.R — sources util.R and exercises its functions.
library(stats)
source("util.R")

RADIUS <- 3

run <- function() {
  a <- area(RADIUS)
  d <- twice(a)
  d
}

run()
