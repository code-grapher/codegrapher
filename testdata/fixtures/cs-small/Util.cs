using Svc = CsSmall.App.Service;

namespace CsSmall.Util;

public class Kennel
{
    public string Boast()
    {
        var s = new Svc();
        return s.Describe();
    }
}
