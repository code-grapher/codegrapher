package com.example.shapes;

/** Base shape holding a name. */
public abstract class Base implements Shape {
    protected String name;

    public Base(String name) {
        this.name = name;
    }

    @Override
    public String label() {
        return name;
    }
}
