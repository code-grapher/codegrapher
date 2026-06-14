/* main.c builds a Shape and reports its area via the shape.h API. */
#include "shape.h"
#include <stdio.h>

/* run builds a circle and prints its area and label. */
double run(void) {
    struct Shape c;
    c.radius = 2.0;
    c.kind = ROUND;
    double a = area(&c);
    const char *name = label(&c);
    printf("%s %f\n", name, a);
    return a;
}
