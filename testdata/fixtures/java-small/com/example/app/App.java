package com.example.app;

import com.example.shapes.Circle;

/** Entry point exercising cross-package import + new + method call. */
public class App {
    public double run() {
        Circle c = new Circle("unit", 1.0);
        double a = c.area();
        return a;
    }
}
