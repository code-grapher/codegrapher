package com.example.shapes;

/** A circle. */
public class Circle extends Base {
    public static final double PI = 3.14159;

    private final double radius;

    public Circle(String name, double radius) {
        super(name);
        this.radius = radius;
    }

    @Override
    public double area() {
        return PI * radius * radius;
    }
}
