package example;

public class App {
    public static void main(String[] args) {
        System.out.println(greeting("build-brief"));
    }

    static String greeting(String name) {
        return "hello, " + name;
    }
}
