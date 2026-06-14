/* shape.h declares the Shape struct and its operations. */
#ifndef SHAPE_H
#define SHAPE_H

#define PI 3.14159

/* Kind enumerates the supported shape families. */
enum Kind { ROUND = 0, POLYGON = 1 };

/* Shape is a circle described by its radius. */
struct Shape {
    double radius;
    enum Kind kind;
};

/* area returns the area of a Shape. */
double area(struct Shape *s);

/* label returns a human-readable name for a Shape. */
const char *label(struct Shape *s);

#endif /* SHAPE_H */
