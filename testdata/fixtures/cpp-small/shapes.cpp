// shapes.cpp defines the out-of-line members declared in shapes.hpp and a
// driver that constructs shapes and calls their methods.
#include "shapes.hpp"

namespace geo {

Point::Point(double x, double y) : x_(x), y_(y) {}

double Point::distanceTo(const Point& other) const {
    double dx = scale(x_ - other.x_, 1.0);
    return dx;
}

Shape::~Shape() {}

Circle::Circle(double radius) : radius_(radius) {}

double Circle::area() const {
    return scale(radius_, radius_);
}

} // namespace geo

// run constructs shapes and exercises the virtual call.
double run() {
    using namespace geo;
    Point p(1.0, 2.0);
    Shape* s = new Circle(3.0);
    double a = s->area();
    double d = p.distanceTo(p);
    return a + d;
}
